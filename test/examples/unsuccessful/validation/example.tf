provider "aws" {
  region = "us-east-1"
}

resource "aws_instance" "redis" {
  ami = "ami-80861296"
  instance_type = "t2.micro"
  key_name = "${var.key_name}"
  count = 1

  provisioner "habitat" {
    peer = "${aws_instance.redis.0.private_ip}"
    use_sudo = true
    service_type = "not-a-real-service-type"

    service {
      name = "core/redis"
      topology = "faketopology"
      strategy = "fakestrategy"
      user_toml = "${file("conf/redis.toml")}"
    }

    connection {
      type     = "ssh"
      user = "ubuntu"
      private_key = "${file("${var.key_path}")}"
    }
  }
}
