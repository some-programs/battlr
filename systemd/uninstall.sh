#!/bin/bash

if [ ! $UID == 0 ]; then
  echo "root privileges required"
  exit 1
fi

set -e
set +x
systemctl disable battlr || true
systemctl stop  battlr || true
rm -f /etc/systemd/system/battlr.service
rm -f /usr/local/bin/battlr
