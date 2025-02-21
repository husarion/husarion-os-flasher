setup-go:
    #!/bin/bash
    # check if running as root
    if [ "$EUID" -ne 0 ]; then
        echo "Please run as root"
        exit
    fi

    GO_VERSION=1.24.0

    wget https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz -O /tmp/go.tar.gz
    rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tar.gz

    export PATH=$PATH:/usr/local/go/bin
    go version

build:
    #!/bin/bash
    export PATH=$PATH:/usr/local/go/bin
    go build -o husarion-os-flasher