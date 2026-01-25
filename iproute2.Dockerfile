FROM ubuntu:latest

RUN apt update -y && apt install -y iproute2
