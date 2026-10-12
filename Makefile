SHELL := /usr/bin/env bash

GOLIB ?= golib

.PHONY: source-docs-check check check-full ci cohesion config inventory repository-check workflows

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

check: source-docs-check
	$(GOLIB) check --local --all

check-full: source-docs-check
	$(GOLIB) check --all

ci: source-docs-check config inventory repository-check workflows cohesion check

source-docs-check:
	$(GOLIB) source-docs check
