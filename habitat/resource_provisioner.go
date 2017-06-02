package habitat

import (
	"errors"
	"fmt"
	"io"
	"log"
	"path"
	"strings"
	"time"

	"github.com/hashicorp/terraform/communicator"
	"github.com/hashicorp/terraform/communicator/remote"
	"github.com/hashicorp/terraform/terraform"
	linereader "github.com/mitchellh/go-linereader"
	"github.com/mitchellh/mapstructure"
)

const install_url = "https://raw.githubusercontent.com/habitat-sh/habitat/master/components/hab/install.sh"

var serviceTypes = map[string]bool{"unmanaged": true, "systemd": true}
var updateStrategies = map[string]bool{"at-once": true, "rolling": true, "none": true}
var topologies = map[string]bool{"leader": true, "standalone": true}

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
	ServiceType   string    `mapstructure:"service_type"`
}

type Service struct {
	Name        string   `mapstructure:"name"`
	Strategy    string   `mapstructure:"strategy"`
	Topology    string   `mapstructure:"topology"`
	Channel     string   `mapstructure:"channel"`
	Group       string   `mapstructure:"group"`
	URL         string   `mapstructure:"url"`
	Binds       []Bind   `mapstructure:"bind"`
	BindStrings []string `mapstructure:"binds"`
	UserTOML    string   `mapstructure:"user_toml"`
}

type Bind struct {
	Alias   string `mapstructure:"alias"`
	Service string `mapstructure:"service"`
	Group   string `mapstructure:"group"`
}

func (s *Service) getPackageName(full_name string) string {
	return strings.Split(full_name, "/")[1]
}

func (b *Bind) toBindString() string {
	return fmt.Sprintf("%s:%s.%s", b.Alias, b.Service, b.Group)
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

func (r *ResourceProvisioner) Validate(c *terraform.ResourceConfig) ([]string, []error) {
	var warn []string
	var error []error

	p, err := r.decodeConfig(c)
	if err != nil {
		error = append(error, err)
		// Failed to decode the config, so there is no provisioner to run anymore validations against.
		return warn, error
	}

	if p.ServiceType != "" {
		if !serviceTypes[p.ServiceType] {
			error = append(error, errors.New(p.ServiceType+" is not a valid service_type."))
		}
	}

	// Loop through all defined services and validate configs
	if p.Services != nil {
		for _, service := range p.Services {
			// Validating individual services
			if service.Strategy != "" {
				if !updateStrategies[service.Strategy] {
					error = append(error, errors.New(service.Strategy+" is not a valid update strategy."))
				}
			}

			if service.Topology != "" {
				if !topologies[service.Topology] {
					error = append(error, errors.New(service.Topology+" is not a valid topology."))
				}
			}
		}
	}

	return warn, error
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

	if p.ServiceType == "" {
		p.ServiceType = "unmanaged"
	}

	for i, service := range p.Services {
		for _, bs := range service.BindStrings {
			tb, err := getBindFromString(bs)
			if err != nil {
				return nil, err
			}
			p.Services[i].Binds = append(p.Services[i].Binds, tb)
		}
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

	switch p.ServiceType {
	case "unmanaged":
		return p.startHabUnmanaged(o, comm, options)
	case "systemd":
		return p.startHabSystemd(o, comm, options)
	default:
		return err
	}
}

func (p *Provisioner) startHabUnmanaged(o terraform.UIOutput, comm communicator.Communicator, options string) error {
	// Create the sup directory for the log file
	var command string
	if p.UseSudo {
		command = "sudo mkdir -p /hab/sup/default && sudo chmod o+w /hab/sup/default"
	} else {
		command = "mkdir -p /hab/sup/default && chmod o+w /hab/sup/default"
	}
	err := p.runCommand(o, comm, command)
	if err != nil {
		return err
	}

	if p.UseSudo {
		command = fmt.Sprintf("(setsid sudo hab sup run %s > /hab/sup/default/sup.log 2>&1 &) ; sleep 1", options)
	} else {
		command = fmt.Sprintf("(setsid hab sup run %s > /hab/sup/default/sup.log 2>&1 <&1 &) ; sleep 1", options)
	}
	return p.runCommand(o, comm, command)
}

func (p *Provisioner) startHabSystemd(o terraform.UIOutput, comm communicator.Communicator, options string) error {
	systemd_unit := `[Unit]
Description=Habitat Supervisor

[Service]
ExecStart=/bin/hab sup run %s
Restart=on-failure

[Install]
WantedBy=default.target`

	systemd_unit = fmt.Sprintf(systemd_unit, options)
	var command string
	if p.UseSudo {
		command = fmt.Sprintf("sudo echo '%s' | sudo tee /etc/systemd/system/hab-supervisor.service > /dev/null", systemd_unit)
	} else {
		command = fmt.Sprintf("echo '%s' | tee /etc/systemd/system/hab-supervisor.service > /dev/null", systemd_unit)
	}

	err := p.runCommand(o, comm, command)
	if err != nil {
		return err
	}

	if p.UseSudo {
		command = fmt.Sprintf("sudo systemctl start hab-supervisor")
	} else {
		command = fmt.Sprintf("systemctl start hab-supervisor")
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
		options += fmt.Sprintf(" --bind %s", bind.toBindString())
	}
	command = fmt.Sprintf("hab sup start %s %s", service.Name, options)
	if p.UseSudo {
		command = fmt.Sprintf("sudo %s", command)
	}
	return p.runCommand(o, comm, command)
}

func (p *Provisioner) uploadUserTOML(o terraform.UIOutput, comm communicator.Communicator, service Service) error {
	// Create the hab svc directory to lay down the user.toml before loading the service
	destDir := fmt.Sprintf("/hab/svc/%s", service.getPackageName(service.Name))
	command := fmt.Sprintf("mkdir -p %s", destDir)
	if p.UseSudo {
		command = fmt.Sprintf("sudo %s", command)
	}
	err := p.runCommand(o, comm, command)
	if err != nil {
		return err
	}

	// Use tee to lay down user.toml instead of the communicator file uploader to get around permissions issues.
	command = fmt.Sprintf("sudo echo '%s' | sudo tee %s > /dev/null", service.UserTOML, path.Join(destDir, "user.toml"))
	fmt.Println("Command: " + command)
	o.Output("Command: " + command)
	if p.UseSudo {
		command = fmt.Sprintf("sudo echo '%s' | sudo tee %s > /dev/null", service.UserTOML, path.Join(destDir, "user.toml"))
	} else {
		command = fmt.Sprintf("echo '%s' | tee %s > /dev/null", service.UserTOML, path.Join(destDir, "user.toml"))
	}
	return p.runCommand(o, comm, command)
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

func getBindFromString(bind string) (Bind, error) {
	t := strings.FieldsFunc(bind, func(d rune) bool {
		switch d {
		case ':', '.':
			return true
		}
		return false
	})
	if len(t) != 3 {
		return Bind{}, errors.New("Invalid bind specification: " + bind)
	}
	return Bind{Alias: t[0], Service: t[1], Group: t[2]}, nil
}
