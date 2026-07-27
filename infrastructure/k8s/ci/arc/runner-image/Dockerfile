ARG RUNNER_BASE_IMAGE=ghcr.io/actions/actions-runner:2.334.0@sha256:b6614fce332517f74d0a76e7c762fb08e4f2ff13dcf333183397c8a5725b6e8e
FROM ${RUNNER_BASE_IMAGE}

USER root

ARG GO_VERSION=1.26.2
ARG GO_LINUX_AMD64_SHA256=990e6b4bbba816dc3ee129eaeaf4b42f17c2800b88a2166c265ac1a200262282

ADD https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz /tmp/go.tar.gz

RUN echo "${GO_LINUX_AMD64_SHA256}  /tmp/go.tar.gz" | sha256sum --check --status \
    && tar -C /usr/local -xzf /tmp/go.tar.gz \
    && rm /tmp/go.tar.gz

ENV PATH="/usr/local/go/bin:${PATH}"

USER runner
