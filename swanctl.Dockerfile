FROM ubuntu:latest

WORKDIR /

RUN apt update -y && apt install -y strongswan-swanctl
