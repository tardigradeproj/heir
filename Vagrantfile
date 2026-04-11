
Vagrant.configure("2") do |config|
  config.vm.box = "hashicorp-education/ubuntu-24-04"
  config.vm.disk :disk, size: "25GB", primary: true

  config.vm.synced_folder "./", "/home/vagrant/samaritano", type: "rsync",
    rsync__exclude: [".git/", ".DS_Store", "vendor/"]

  config.vm.provision "shell", name: "kubectl", inline: <<-SHELL
    ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
    KUBE_VERSION=$(curl -fsSL https://dl.k8s.io/release/stable.txt)
    curl -fsSL "https://dl.k8s.io/release/${KUBE_VERSION}/bin/linux/${ARCH}/kubectl" -o /usr/local/bin/kubectl
    chmod +x /usr/local/bin/kubectl
  SHELL

  config.vm.provision "shell", name: "docker", inline: <<-SHELL
    apt-get update -q
    apt-get install -y -q ca-certificates curl gnupg
    install -m 0755 -d /etc/apt/keyrings
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
    chmod a+r /etc/apt/keyrings/docker.gpg
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
      https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
      > /etc/apt/sources.list.d/docker.list
    apt-get update -q
    apt-get install -y -q docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
    usermod -aG docker vagrant
  SHELL

  config.vm.provision "shell", name: "kind", inline: <<-SHELL
    ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
    KIND_VERSION=$(curl -fsSL https://api.github.com/repos/kubernetes-sigs/kind/releases/latest | grep '"tag_name"' | cut -d'"' -f4)
    curl -fsSL "https://kind.sigs.k8s.io/dl/${KIND_VERSION}/kind-linux-${ARCH}" -o /usr/local/bin/kind
    chmod +x /usr/local/bin/kind
  SHELL

  config.vm.provision "shell", name: "golang", inline: <<-SHELL
    ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
    curl -fsSL "https://go.dev/dl/go1.25.3.linux-${ARCH}.tar.gz" -o /tmp/go.tar.gz
    rm -rf /usr/local/go
    tar -C /usr/local -xzf /tmp/go.tar.gz
    rm /tmp/go.tar.gz
    echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/golang.sh
    echo 'export GOPATH=/home/vagrant/go' >> /etc/profile.d/golang.sh
    echo 'export PATH=$PATH:$GOPATH/bin' >> /etc/profile.d/golang.sh

    GO_PATH_LINE='export PATH=$PATH:/usr/local/go/bin'
    for profile in /etc/profile.d/go.sh ~/.profile ~/.bashrc; do
      if ! grep -qF "$GO_PATH_LINE" "$profile" 2>/dev/null; then
        echo "$GO_PATH_LINE" | sudo tee -a "$profile" > /dev/null
      fi
    done
  SHELL
  config.vm.provision "shell", name: "provision-kind-cluster", inline: <<-SHELL
    kind create cluster --name samaritano
  SHELL

  config.vm.provision "shell", name: "resize-fs", inline: <<-SHELL
    sudo lvextend -l +100%FREE /dev/mapper/ubuntu--vg-ubuntu--lv
    sudo resize2fs /dev/mapper/ubuntu--vg-ubuntu--lv
  SHELL

end
