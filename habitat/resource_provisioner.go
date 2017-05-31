package habitat

import (
	"fmt"
	"io"
	"log"
	"time"

	"strings"

	"path"

	"github.com/hashicorp/terraform/communicator"
	"github.com/hashicorp/terraform/communicator/remote"
	"github.com/hashicorp/terraform/terraform"
	linereader "github.com/mitchellh/go-linereader"
	"github.com/mitchellh/mapstructure"
)

const install_url = "https://raw.githubusercontent.com/habitat-sh/habitat/master/components/hab/install.sh"

type Provisioner struct {
	Version       string    `mapstructure:"version"`
	Services      []Service `mapstructure:"service"`
	PermanentPeer bool      `mapstructure:"permanent_peer"`
	ListenGossip  string    `mapstructure:"listen_gossip"`
	ListenHTTP    string    `mapstructure:"listen_http"`
	Peer          string    `mapstructure:"peer"`
	RingKey       string    `mapstructure:"ring_key"`
	SkipInstall   bool      `mapstructure:"skip_hab_install"`
	UseSudo       bool      `mapstructure:"use_sudo"`
}

type Service struct {
	Name     string   `mapstructure:"name"`
	Strategy string   `mapstructure:"strategy"`
	Topology string   `mapstructure:"topology"`
	Channel  string   `mapstructure:"channel"`
	Group    string   `mapstructure:"group"`
	URL      string   `mapstructure:"url"`
	Binds    []string `mapstructure:"binds"`
	UserTOML string   `mapstructure:"user_toml"`
}

func (s *Service) getPackageName(full_name string) string {
	return strings.Split(full_name, "/")[1]
}

type ResourceProvisioner struct{}

func (p *Provisioner) Run(o terraform.UIOutput, comm communicator.Communicator) error {
	return nil
}

func (p *Provisioner) Validate() error {
	return nil
}

func (r *ResourceProvisioner) Apply(
	o terraform.UIOutput,
	s *terraform.InstanceState,
	c *terraform.ResourceConfig) error {

	p, err := r.decodeConfig(c)
	if err != nil {
		return err
	}

	comm, err := communicator.New(s)
	if err != nil {
		o.Output("Couldn't open comms")
	}

	err = retryFunc(comm.Timeout(), func() error {
		err := comm.Connect(o)
		return err
	})
	if err != nil {
		return err
	}
	defer comm.Disconnect()

	if !p.SkipInstall {
		if err := p.installHab(o, comm); err != nil {
			return err
		}
	}

	if err := p.startHab(o, comm); err != nil {
		return err
	}

	if p.Services != nil {
		for _, service := range p.Services {
			if err := p.startHabService(o, comm, service); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *ResourceProvisioner) Validate(c *terraform.ResourceConfig) ([]string, []error) {
	fmt.Println("Validating!")
	return nil, nil
}

func (p *ResourceProvisioner) Stop() error {
	return nil
}

func (r *ResourceProvisioner) decodeConfig(c *terraform.ResourceConfig) (*Provisioner, error) {
	p := new(Provisioner)

	conf := &mapstructure.DecoderConfig{
		ErrorUnused:      false,
		WeaklyTypedInput: true,
		Result:           p,
	}

	decoder, err := mapstructure.NewDecoder(conf)
	if err != nil {
		return nil, err
	}

	m := make(map[string]interface{})

	for k, v := range c.Raw {
		m[k] = v
	}

	for k, v := range c.Config {
		m[k] = v
	}

	if err := decoder.Decode(m); err != nil {
		return nil, err
	}

	return p, nil
}

// TODO:  Add proxy support
func (p *Provisioner) installHab(o terraform.UIOutput, comm communicator.Communicator) error {
	// Build the install command
	command := fmt.Sprintf("curl -L0 %s > install.sh", install_url)
	err := p.runCommand(o, comm, command)
	if err != nil {
		return err
	}

	// Run the install script
	if p.Version == "" {
		command = fmt.Sprintf("bash ./install.sh ")
	} else {
		command = fmt.Sprintf("bash ./install.sh -v %s", p.Version)
	}

	if p.UseSudo {
		command = fmt.Sprintf("sudo %s", command)
	}

	err = p.runCommand(o, comm, command)
	if err != nil {
		return err
	}

	err = p.createHabUser(o, comm)
	if err != nil {
		return err
	}

	return p.runCommand(o, comm, fmt.Sprintf("rm -f install.sh"))
}

// TODO: Add support for options
func (p *Provisioner) startHab(o terraform.UIOutput, comm communicator.Communicator) error {
	// Install the supervisor first
	var command string
	if p.Version == "" {
		command += fmt.Sprintf("hab install core/hab-sup")
	} else {
		command += fmt.Sprintf("hab install core/hab-sup/%s", p.Version)
	}

	if p.UseSudo {
		command = fmt.Sprintf("sudo -E %s", command)
	}

	command = fmt.Sprintf("env HAB_NONINTERACTIVE=true %s", command)

	err := p.runCommand(o, comm, command)
	if err != nil {
		return err
	}

	// Build up sup options
	options := ""
	if p.PermanentPeer {
		options += " -I"
	}

	if p.ListenGossip != "" {
		options += fmt.Sprintf(" --listen-gossip %s", p.ListenGossip)
	}

	if p.ListenHTTP != "" {
		options += fmt.Sprintf(" --listen-http %s", p.ListenHTTP)
	}

	if p.Peer != "" {
		options += fmt.Sprintf(" --peer %s", p.Peer)
	}

	if p.RingKey != "" {
		options += fmt.Sprintf(" --ring %s", p.RingKey)
	}

	// Create the sup directory for the log file
	if p.UseSudo {
		command = "sudo mkdir -p /hab/sup/default && sudo chmod o+w /hab/sup/default"
	} else {
		command = "mkdir -p /hab/sup/default && chmod o+w /hab/sup/default"
	}
	err = p.runCommand(o, comm, command)
	if err != nil {
		return err
	}

	if p.UseSudo {
		command = fmt.Sprintf("(setsid sudo hab sup run %s > /hab/sup/default/sup.log 2>&1 &) ; sleep 1", options)
	} else {
		command = fmt.Sprintf("(nohup hab sup run %s > /hab/sup/default/sup.log 2>&1 <&1 & disown) ; sleep 1", options)
	}
	return p.runCommand(o, comm, command)
}

func (p *Provisioner) createHabUser(o terraform.UIOutput, comm communicator.Communicator) error {
	// Create the hab user
	command := fmt.Sprintf("hab install core/busybox")
	if p.UseSudo {
		command = fmt.Sprintf("sudo %s", command)
	}
	err := p.runCommand(o, comm, command)
	if err != nil {
		return err
	}

	command = fmt.Sprintf("hab pkg exec core/busybox adduser -D -g \"\" hab")
	if p.UseSudo {
		command = fmt.Sprintf("sudo %s", command)
	}
	return p.runCommand(o, comm, command)
}

func (p *Provisioner) startHabService(o terraform.UIOutput, comm communicator.Communicator, service Service) error {
	var command string
	if p.UseSudo {
		command = fmt.Sprintf("env HAB_NONINTERACTIVE=true sudo -E hab pkg install %s", service.Name)
	} else {
		command = fmt.Sprintf("env HAB_NONINTERACTIVE=true hab pkg install %s", service.Name)
	}
	err := p.runCommand(o, comm, command)
	if err != nil {
		return err
	}

	err = p.uploadUserTOML(o, comm, service)
	if err != nil {
		return err
	}

	options := ""
	if service.Topology != "" {
		options += fmt.Sprintf(" --topology %s", service.Topology)
	}

	if service.Strategy != "" {
		options += fmt.Sprintf(" --strategy %s", service.Strategy)
	}

	if service.Channel != "" {
		options += fmt.Sprintf(" --channel %s", service.Channel)
	}

	if service.URL != "" {
		options += fmt.Sprintf("--url %s", service.URL)
	}

	if service.Group != "" {
		options += fmt.Sprintf(" --group %s", service.Group)
	}

	for _, bind := range service.Binds {
		options += fmt.Sprintf(" --bind %s", bind)
	}
	command = fmt.Sprintf("hab sup start %s %s", service.Name, options)
	if p.UseSudo {
		command = fmt.Sprintf("sudo %s", command)
	}
	return p.runCommand(o, comm, command)
}

func (p *Provisioner) uploadUserTOML(o terraform.UIOutput, comm communicator.Communicator, service Service) error {
	destDir := fmt.Sprintf("/hab/svc/%s", service.getPackageName(service.Name))
	command := fmt.Sprintf("mkdir -p %s ; sudo chmod o+w %[1]s", destDir)
	if p.UseSudo {
		command = fmt.Sprintf("sudo %s", command)
	}
	err := p.runCommand(o, comm, command)
	if err != nil {
		return err
	}

	userToml := strings.NewReader(service.UserTOML)
	return comm.Upload(path.Join(destDir, "user.toml"), userToml)
}

func retryFunc(timeout time.Duration, f func() error) error {
	finish := time.After(timeout)

	for {
		err := f()
		if err == nil {
			return nil
		}
		log.Printf("Retryable error: %v", err)

		select {
		case <-finish:
			return err
		case <-time.After(3 * time.Second):
		}
	}
}

func (p *Provisioner) copyOutput(o terraform.UIOutput, r io.Reader, doneCh chan<- struct{}) {
	defer close(doneCh)
	lr := linereader.New(r)
	for line := range lr.Ch {
		o.Output(line)
	}
}

func (p *Provisioner) runCommand(o terraform.UIOutput, comm communicator.Communicator, command string) error {
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()

	outDoneCh := make(chan struct{})
	errDoneCh := make(chan struct{})
	go p.copyOutput(o, outR, outDoneCh)
	go p.copyOutput(o, errR, errDoneCh)

	cmd := &remote.Cmd{
		Command: command,
		Stdout:  outW,
		Stderr:  errW,
	}

	err := comm.Start(cmd)
	if err != nil {
		return fmt.Errorf("Error executing command %q: %v", cmd.Command, err)
	}

	cmd.Wait()
	if cmd.ExitStatus != 0 {
		err = fmt.Errorf(
			"Command %q exited with non-zero exit status: %d", cmd.Command, cmd.ExitStatus)
	}

	outW.Close()
	errW.Close()
	<-outDoneCh
	<-errDoneCh

	return err
}
