# Smoke Tests

This Subdirectory contains smoke tests used for E2E Verification.

It has 3 main goals:

- Be fast enough to execute in PRs
- Be small enough to quickly iterate
- Be comprehensive enough to serve as a middle-ground between integration tests and e2e tests

## Contents

This Repo contains a `Makefile` which will execute `go test` against a smoke-test running
with [The official Kubernetes E2E Testing Framework](https://github.com/kubernetes-sigs/e2e-framework).

## Run the Tests

Simply run `make` and let the magic happen!