#!/usr/bin/env bash

export PATH="$HOME/.local/bin:$PATH"
# shellcheck disable=SC1091
source "${CONTROL_VENV_DIR:-$HOME/.venvs/go-coffeeshop-platform}/bin/activate"
export AWS_DEFAULT_REGION=ap-southeast-1
export ANSIBLE_PRIVATE_KEY_FILE="$HOME/.ssh/coffeeshop-dev.pem"
export TF_DATA_DIR="${TERRAFORM_DATA_DIR:-$HOME/.cache/go-coffeeshop/terraform/dev}"


echo "Go-Coffeeshop platform environment activated!"
echo "AWS Region: $AWS_DEFAULT_REGION"
echo "Ansible Key File: $ANSIBLE_PRIVATE_KEY_FILE"
echo "Terraform Data Directory: $TF_DATA_DIR"
