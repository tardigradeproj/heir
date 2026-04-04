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
        run_command(f'openssl genrsa -out {svr}.key 4096')
        run_command(f'openssl req -new -key {svr}.key -sha256 -config ca.conf -section {svr} -out {svr}.csr')
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
                    f'--kubeconfig={svr}.kubeconfig')

        run_command(f' kubectl config set-credentials system:{svr} '
                    f'--client-certificate={svr}.crt '
                    f'--client-key={svr}.key '
                    f'--embed-certs=true '
                    f'--kubeconfig={svr}.kubeconfig')

        run_command(f'kubectl config set-context default '
                    f'--cluster=tardigrade '
                    f'--user=system:{svr} '
                    f'--kubeconfig={svr}.kubeconfig')

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