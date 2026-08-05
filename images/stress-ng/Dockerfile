FROM ubuntu:24.04

ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
  && apt-get install -y --no-install-recommends \
  stress-ng \
  && rm -rf /var/lib/apt/lists/*

COPY entrypoint.sh /usr/local/bin/stress-ng-wrapper

RUN chmod +x /usr/local/bin/stress-ng-wrapper

ENTRYPOINT ["/usr/local/bin/stress-ng-wrapper"]