# Makefile for azkey-bot-roumu
# Variables
IMAGE_NAME := azkey-bot-roumu
DATA_DIR := $(PWD)/data
ENV_FILE := .env
ROUMU_DATA_DIR := /app/data

# Build targets
.PHONY: build
build:
	docker build --no-cache -t $(IMAGE_NAME) .

# Single command execution
.PHONY: run
run:
	@if [ -z "$(CMD)" ]; then echo "Usage: make run CMD=<command>"; exit 1; fi
	docker run --rm --env-file $(ENV_FILE) -e ROUMU_DATA_DIR=$(ROUMU_DATA_DIR) -v $(DATA_DIR):/app/data $(IMAGE_NAME) $(CMD)

# Common commands
.PHONY: status
status:
	docker run --rm --env-file $(ENV_FILE) -e ROUMU_DATA_DIR=$(ROUMU_DATA_DIR) -v $(DATA_DIR):/app/data $(IMAGE_NAME) status

.PHONY: follow
follow:
	docker run --rm --env-file $(ENV_FILE) -e ROUMU_DATA_DIR=$(ROUMU_DATA_DIR) -v $(DATA_DIR):/app/data $(IMAGE_NAME) follow

.PHONY: check
check:
	docker run --rm --env-file $(ENV_FILE) -e ROUMU_DATA_DIR=$(ROUMU_DATA_DIR) -v $(DATA_DIR):/app/data $(IMAGE_NAME) check

.PHONY: timeline
timeline:
	@LIMIT_ARG=""; if [ -n "$(LIMIT)" ]; then LIMIT_ARG="--limit $(LIMIT)"; fi; \
	docker run --rm --env-file $(ENV_FILE) -e ROUMU_DATA_DIR=$(ROUMU_DATA_DIR) -v $(DATA_DIR):/app/data $(IMAGE_NAME) timeline $$LIMIT_ARG

.PHONY: dakoku
dakoku:
	@if [ -z "$(USER_ID)" ]; then echo "Usage: make dakoku USER_ID=<user_id>"; exit 1; fi
	docker run --rm --env-file $(ENV_FILE) -e ROUMU_DATA_DIR=$(ROUMU_DATA_DIR) -v $(DATA_DIR):/app/data $(IMAGE_NAME) dakoku --user-id $(USER_ID)

.PHONY: reset
reset:
	docker run --rm --env-file $(ENV_FILE) -e ROUMU_DATA_DIR=$(ROUMU_DATA_DIR) -v $(DATA_DIR):/app/data $(IMAGE_NAME) reset

.PHONY: serve
serve:
	docker run --rm --env-file $(ENV_FILE) -e ROUMU_DATA_DIR=$(ROUMU_DATA_DIR) -v $(DATA_DIR):/app/data $(IMAGE_NAME) serve

# Docker Compose operations
.PHONY: compose-up
compose-up:
	docker compose build --no-cache
	docker compose run --rm --build azkey-bot-roumu check

.PHONY: compose-down
compose-down:
	docker compose down

# Cleanup
.PHONY: clean
clean: compose-down
	docker rmi $(IMAGE_NAME) || true
