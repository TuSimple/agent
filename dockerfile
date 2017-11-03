FROM rancher/server:latest

COPY dist/artifacts/go-agent.tar.gz /usr/share/cattle/artifacts/

