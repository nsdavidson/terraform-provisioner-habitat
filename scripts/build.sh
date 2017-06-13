#!/usr/bin/env bash

BUILD_ARCH="386 amd64"
BUILD_OS="linux darwin windows"
EXCLUDED_OSARCH="!darwin/386"

rm -rf ../dist/
mkdir -p ../dist/

cd ..
gox -os="${BUILD_OS}" -arch="${BUILD_ARCH}" -osarch="${EXCLUDED_OSARCH}" -output "dist/terraform-provisioner-habitat-{{.OS}}-{{.Arch}}"