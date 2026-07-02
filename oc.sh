#!/bin/bash
# Wrapper script for oc with CRC environment
eval $(crc oc-env)
exec oc "$@"
