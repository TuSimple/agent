agent
========

This agent runs on compute nodes in a Rancher cluster. It receives events from the Rancher server, acts upon them, and returns response events.

And we intend to add gpu support to it！

## Building

```
$ sudo make
$ sudo docker build -t registry.bj.tusimple.ai:5043/service/rancher/server .
```

## Running
1. Treat it as normal rancher/server and upgrade
2. Host with gpu resource to be added need to first install nvidia-docker and use docker command to check if nvidia-docker volume was properly installed
```
# Install nvidia-docker and nvidia-docker-plugin
$ wget -P /tmp https://github.com/NVIDIA/nvidia-docker/releases/download/v1.0.1/nvidia-docker_1.0.1-1_amd64.deb
$ sudo dpkg -i /tmp/nvidia-docker*.deb && rm /tmp/nvidia-docker*.deb

# Test nvidia-smi
$ nvidia-docker run --rm nvidia/cuda nvidia-smi

# Check nvidia-docker volume
$ docker volume ls | grep nvidia-docker
```
3. When adding host with gpu, you need select 'Add Label', and add 'gpuReservation = {amount of gpu cards}'
4. When adding service that need gpu, you need to add label: 'gpu={gpu you need}, ratio={ratio of every the gpu card you need(from scale 1 to 10)}'. And please don't add label 'gpu_card' to it.