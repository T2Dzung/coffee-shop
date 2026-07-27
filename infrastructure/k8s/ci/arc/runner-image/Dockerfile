ARG RUNNER_BASE_IMAGE=ghcr.io/actions/actions-runner:2.334.0@sha256:b6614fce332517f74d0a76e7c762fb08e4f2ff13dcf333183397c8a5725b6e8e
FROM ${RUNNER_BASE_IMAGE}

USER root

RUN apt-get update \
    && apt-get install --yes --no-install-recommends make=4.3-4.1build2 \
    && rm -rf /var/lib/apt/lists/*

USER runner
