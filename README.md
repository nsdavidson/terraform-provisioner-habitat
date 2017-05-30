# terraform-provisioner-habitat
A [Habitat](https://habitat.sh) provisioner for [Terraform](https://terraform.io)

## Installation
### Download binaries
Builds for macOS, Linux, and Windows are attached to GitHub releases.

### Build from source
```bash
git clone git@github.com:nsdavidson/terraform-provisioner-habitat.git
cd terraform-provisioner-habitat
go build
```

After getting or building a copy of the provisioner plugin, add the following to your ~/.terraformrc (create the file if it doesn't exist)
```
provisioners {
  habitat = "<path to the plugin binary>"
}
```

## Requirements
* This provisioner will currently only work on Linux targets.  As the Habitat supervisor becomes available on more systems, support for those will be added.
* Currently, we assume several userspace utilities on the target system (curl, wget, useradd, setsid, etc).  
* You must have SSH access as root or a user that can passwordless sudo.

## Usage
Example to spin up a 3 node redis cluster:
```hcl
resource "aws_instance" "redis" {
  ami = "ami-12345"
  instance_type = "t2.micro"
  key_name = "foo"
  count = 3

  provisioner "habitat" {
    peer = "${aws_instance.redis.0.private_ip}"
    use_sudo = true
    
    service {
      name = "core/redis"
      topology = "leader"
      user_toml = "${file("conf/redis.toml")}"
    }
  }
}
```

## Attributes
There are 2 configuration levels, supervisor and service.  Values placed directly within the `provisioner` block are supervisor configs, and values placed inside a `service` block are service configs.
### Supervisor
* `version`: The version of Habitat to install.  Optional (Defaults to latest)
* `permanent_peer`: Whether this supervisor should be marked as a permanent peer. Optional (Defaults to false)
* `listen_gossip`: IP and port to listen for gossip traffic.  Optional (Defaults to "0.0.0.0:9638")
* `listen_http`: IP and port for the HTTP API service.  Optional (Defaults to "0.0.0.0:9631")
* `peer`: IP or FQDN of a supervisor instance to peer with.  Optional (Defaults to none)
* `ring_key`: Key for encrypting the supervisor ring traffic.  Optional (Defaults to none)
* `skip_install`: Skips the installation Habitat, if it's being installed another way.  Optional (Defaults to no)
* `use_sudo`: Use sudo to execute commands on the target system. Optional (Defaults to false)

### Service
* `name`: A package identifier of the Habitat package to start (eg `core/nginx`, `core/nginx/1.11.10` or `core/nginx/1.11.10/20170215233218`).  Required.
* `strategy`: Update strategy to use. Possible values "at-once", "rolling" or "none".  Optional (Defaults to "none")
* `topology`: Topology to start service in.  Possible values "standalone" or "leader".  Optional (Defaults to "standalone")
* `channel`: Channel in a remote depot to watch for package updates.  Optional
* `group`: Service group to join.  Optional (Defaults to "default")
* `url`: URL of the remote Depot to watch.  Optional (Defaults to the public depot)
* `binds`:  Array of binding statements (eg "backend:nginx.default").  Optional
* `user_toml`: TOML formatted user configuration for the service.  Easiest to source from a file (eg `user_toml = "${file("conf/redis.toml")}"`).  Optional