provider "aws" {
  region = "us-east-1"
}

resource "aws_instance" "redis" {
  ami = "ami-80861296"
  instance_type = "t2.micro"
  key_name = "${var.key_name}"
  count = 3

  provisioner "habitat" {
    peer = "${aws_instance.redis.0.private_ip}"
    use_sudo = true
    service_type = "systemd"

    service {
      name = "core/redis"
      topology = "standalone"
      user_toml = "${file("conf/redis.toml")}"
    }

    connection {
      type     = "ssh"
      user = "ubuntu"
      private_key = "${file("${var.key_path}")}"
    }
  }
}

output "ips" {
  value = ["${aws_instance.redis.*.public_ip}"]
}

output "username" {
  value = "ubuntu"
}

output "key_path" {
  value = "${var.key_path}"
}