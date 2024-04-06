#!/bin/bash

if [ ! $UID == 0 ]; then
  echo "root privileges required"
  exit 1
fi

set -e
set -x
systemctl stop battlr || true
cp battlr /usr/local/bin/battlr
chmod 775 /usr/local/bin/battlr
cp battlr.service /etc/systemd/system/battlr.service
chmod 644 /etc/systemd/system/battlr.service
systemctl daemon-reload
systemctl start battlr
systemctl enable battlr
