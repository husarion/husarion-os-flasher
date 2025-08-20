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
    sudo pkill -f husarion-os-flasher
    go build -o husarion-os-flasher

rebuild-on-save:
    #!/bin/bash
    export PATH=$PATH:/usr/local/go/bin
    while true; do
        go build -o husarion-os-flasher && sudo pkill -f './husarion-os-flasher'
        inotifywait -e modify $(find . -name '*.go') || exit
    done

restart-on-build:
    #!/bin/bash
    export PATH=$PATH:/usr/local/go/bin
    while true; do
        ./husarion-os-flasher
    done

reset-changes:
    #!/bin/bash
    branch=$(git rev-parse --abbrev-ref HEAD)

    git fetch origin
    git reset --hard origin/$branch
    git clean -fd

    echo "Local branch '$branch' has been reset to match origin/$branch."
    exit 0
