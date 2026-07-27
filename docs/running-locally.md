
The project contains two make source: Makefile and Makefile.distro. Makefile contains all instruction for heir 
operator setup, while Makefile.distro contain instruction to deal with heir dependencies, such as worker-agent, control plane images, etc...

Pre-requisites 

* goreleaser
* docker
* kind
* vagrant (worker node)


## Download dependency artifacts and build binaries
goreleaser is configured to download heir dependencies, before building anything, as heir embeeds processes such as containerd, kubelet, etc... on its own self.
run: make build-bins

##  Build heir controller manager
make build

## Setup Management cluster

./.local/setup-management-cluster

## Install heir controller on test cluster

make deploy IMG="localhost:5001/controller:latest"

## provision downstream kubernetes cluster

kubectl apply -f .local/heir.yaml

## create join token 

kubectl apply -f .local/heir-worker-join-token.yaml

## copy join token to vagrant

kubectl get secret new-node-join-kubeconfig -o jsonpath={.data.kubeconfig} | vagrant ssh -c <do something to copy the content into /heir/token>

## join vagrant worker

withing the vagrant vm build heir binary:


vagrant ssh -c "cd heir && go build -tags=embedartifacts cmd/distro.go"
vagrant ssh -c "cd heir && sudo ./distro provision worker --token=$(cat token)"

Alternatively you can ssh in vagrant using:
vagrant ssh 
and run 
cd heir
go build -tags=embedartifacts cmd/distro.go
sudo ./distro provision worker --token=$(cat token)



## validate if node joint

kubectl get secret my-cluster-kubeconfig -o jsonpath={.data.kubeconfig} | base64 -d > my-cluster-kubeconfig

kubectl --kubeconfig my-cluster-kubeconfig get nodes
You should see:
NAME             STATUS   ROLES    AGE   VERSION
vagrant-ubuntu   Ready    <none>   42m   v1.35.5

If it does not work try to ssh in vagrant as explain previously to debug the issue.
Supporting commands:
sudo systemctl status heir.service
sudo journalctl -xu heir --since "1 minutes ago"
sudo /var/lib/heir/bin/crictl --runtime-endpoint /run/heir/containerd.sock ps