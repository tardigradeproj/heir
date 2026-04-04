import os
import subprocess
import sys
from typing import Dict, List


def root_ca():

    run_command("openssl genrsa -out ca.key 4096")
    run_command("openssl req -x509 -new -sha512 -noenc -key ca.key -days 3653 -config ca.conf -out ca.crt")

def server_certs():
    servers: List[str] = [ "kube-proxy", "kube-scheduler", "kube-controller-manager", "kube-apiserver", "service-accounts", "admin", "service-accounts"]
    for svr in servers:
        section = "kube-api-server" if svr == "kube-apiserver" else svr
        run_command(f'openssl genrsa -out {svr}.key 4096')
        run_command(f'openssl req -new -key {svr}.key -sha256 -config ca.conf -section {section} -out {svr}.csr')
        run_command(f'openssl x509 -req -days 3653 -in {svr}.csr -copy_extensions '
                    f'copyall -sha256 -CA ca.crt -CAkey ca.key '
                    f'-CAcreateserial -out {svr}.crt')
        # remove csr
        run_command(f"rm {svr}.csr")

def setup_authentication():
    servers: List[str] = [ "kube-scheduler", "kube-controller-manager", "admin"]
    for svr in servers:
        run_command(f'kubectl config set-cluster tardigrade '
                    f'--certificate-authority=ca.crt '
                    f'--embed-certs=true '
                    f'--server=https://127.0.0.1:6443 '
                    f'--kubeconfig={svr}.conf')

        run_command(f' kubectl config set-credentials system:{svr} '
                    f'--client-certificate={svr}.crt '
                    f'--client-key={svr}.key '
                    f'--embed-certs=true '
                    f'--kubeconfig={svr}.conf')

        run_command(f'kubectl config set-context default '
                    f'--cluster=tardigrade '
                    f'--user=system:{svr} '
                    f'--kubeconfig={svr}.conf')
        run_command(f'kubectl config use-context default '
                    f'--kubeconfig={svr}.conf')

def start_local_container() -> str:
    """Start the local-test control plane container, removing any existing one first.

    Returns the container name so it can be passed to copy_to_container.
    """
    container_name = "local-test"
    image = "samaritano-base:v1"

    result = subprocess.run(
        ["docker", "ps", "-a", "--filter", f"name=^{container_name}$", "--format", "{{.Names}}"],
        capture_output=True,
        text=True,
    )
    if container_name in result.stdout:
        print(f"Container '{container_name}' already exists. Removing it...")
        run_command(f"docker rm -f {container_name}")

    cmd = [
        "docker", "run", "-d",
        "--privileged",
        "--cgroupns=host",
        "-v", "/sys/fs/cgroup:/sys/fs/cgroup:rw",
        "--name", container_name,
        image,
    ]
    print(f"Running: {' '.join(cmd)}")
    result = subprocess.run(cmd, check=True, capture_output=True, text=True)
    container_id = result.stdout.strip()
    print(container_id)
    return container_id


def copy_to_container(container_id: str, out_dir: str = "./"):
    """Copy certificates and kubeconfigs into a running container at kubeadm paths.

    Cert/key files  → /etc/kubernetes/pki/
    Kubeconfig files → /etc/kubernetes/
    """
    pki_dir = "/etc/kubernetes/pki"
    k8s_dir = "/etc/kubernetes"

    run_command(f"docker exec {container_id} mkdir -p {pki_dir}")

    for filename in os.listdir(out_dir):
        src = os.path.join(out_dir, filename)
        if not os.path.isfile(src):
            continue

        if filename.endswith(".conf"):
            dest = f"{container_id}:{k8s_dir}/{filename}"
        elif filename.endswith(".crt") or filename.endswith(".key"):
            dest = f"{container_id}:{pki_dir}/{filename}"
        else:
            continue

        run_command(f"docker cp {src} {dest}")

    run_command(f"docker cp ../kube-scheduler.yaml {container_id}:/etc/kubernetes/kube-scheduler.yaml")
    run_command(f"docker exec {container_id} chown -R root:root {k8s_dir}")

def copy_services_to_container(container_id: str, services_dir: str = "../"):
    """Copy *.service files from services_dir into the container as systemd drop-ins.

    Each service file is placed at:
      /etc/systemd/system/<name>.service.d/override.conf
    """
    for filename in os.listdir(services_dir):
        if not filename.endswith(".service"):
            continue

        src = os.path.join(services_dir, filename)
        if not os.path.isfile(src):
            continue

        drop_in_dir = f"/etc/systemd/system/{filename}.d"
        run_command(f"docker exec {container_id} mkdir -p {drop_in_dir}")
        run_command(f"docker cp {src} {container_id}:{drop_in_dir}/override.conf")
        run_command(f"docker exec {container_id} chown root:root {drop_in_dir}/override.conf")



def run_command(command):
    """Helper function to run a shell command and handle errors."""
    try:
        print(f"Running: {command}")
        result = subprocess.run(
            command.split(),
            check=True,
            capture_output=True,
            text=True
        )
        print(result.stdout)
    except subprocess.CalledProcessError as e:
        print(f"Error executing command:\n{e.stderr}", file=sys.stderr)
        sys.exit(1)

if __name__ == '__main__':
    run_command("mkdir -p out")
    run_command("cp ./ca.conf out")
    os.chdir("out")
    root_ca()
    server_certs()
    setup_authentication()
    container_id = start_local_container()
    copy_to_container(container_id, "./")
    copy_services_to_container(container_id)
    run_command(f"docker exec {container_id} systemctl daemon-reload")