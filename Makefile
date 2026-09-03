SHELL := /usr/bin/env bash

GOLIB ?= golib

.PHONY: check ci cohesion config inventory repository-check workflows

config:
	$(GOLIB) config validate

inventory:
	$(GOLIB) inventory

repository-check:
	$(GOLIB) repository check

workflows:
	$(GOLIB) workflows check

cohesion:
	$(GOLIB) cohesion check

check:
	$(GOLIB) check --all

ci: config inventory repository-check workflows cohesion check
