#!/bin/sh
# Sets up the machine of qoqi.dev, from a fresh Ubuntu, and again at will:
# every step can run twice. From the root of the repository:
#
#     make server-setup              # sends deploy/server and install.sh, runs this as root
#
# What it leaves: a firewall (ssh, http, https), ssh by key only, security
# updates applied by themselves, fail2ban, Caddy serving qoqi.dev from
# /var/www/qoqi.dev, and the demo daemon behind demo.qoqi.dev, installed
# by install.sh as anyone would.
set -eu

here="$(cd "$(dirname "$0")" && pwd)"
export DEBIAN_FRONTEND=noninteractive

echo "== packages"
apt-get update -q
apt-get upgrade -yq
apt-get install -yq caddy ufw fail2ban unattended-upgrades rsync curl ca-certificates sqlite3

echo "== firewall"
ufw allow OpenSSH >/dev/null
ufw allow 80/tcp >/dev/null
ufw allow 443/tcp >/dev/null
ufw --force enable

echo "== ssh: keys only"
cat > /etc/ssh/sshd_config.d/10-qoqi.conf <<'CONF'
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
CONF
sshd -t
systemctl reload ssh

echo "== updates, and fail2ban"
dpkg-reconfigure -fnoninteractive unattended-upgrades
systemctl enable --now fail2ban

echo "== the deploy user: sends the site, nothing else"
id deploy >/dev/null 2>&1 || useradd --create-home --shell /bin/bash deploy
install -d -m 700 -o deploy -g deploy /home/deploy/.ssh
touch /home/deploy/.ssh/authorized_keys
# The keys of root may send the site too; a key added by hand (CI) is kept
cat /root/.ssh/authorized_keys /home/deploy/.ssh/authorized_keys | sort -u > /home/deploy/.ssh/keys.new
install -m 600 -o deploy -g deploy /home/deploy/.ssh/keys.new /home/deploy/.ssh/authorized_keys
rm /home/deploy/.ssh/keys.new
install -d -o deploy -g deploy /var/www/qoqi.dev
[ -e /var/www/qoqi.dev/index.html ] || echo '<!doctype html><title>qoqi</title>Soon.' > /var/www/qoqi.dev/index.html

echo "== caddy"
install -m 644 "$here/Caddyfile" /etc/caddy/Caddyfile
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
systemctl enable caddy
systemctl reload-or-restart caddy

echo "== qoqi, from its installer, as anyone would: the latest release"
QOQI_INSTALL=/usr/local/bin sh "$here/install.sh"
id qoqi >/dev/null 2>&1 || useradd --system --home-dir /var/lib/qoqi --shell /usr/sbin/nologin qoqi
install -d -m 700 -o qoqi -g qoqi /var/lib/qoqi
install -m 644 "$here/qoqi-demo.service" /etc/systemd/system/qoqi-demo.service
systemctl daemon-reload
systemctl enable qoqi-demo
# Its first start is with admin, admin: Caddy stops until it has another
first=false
[ -s /root/qoqi-demo-admin ] || first=true
if $first; then systemctl stop caddy; fi
systemctl restart qoqi-demo

# The console starts with admin, admin: it is given a password of its own,
# kept for root alone, to be changed at the first sign in. Caddy starts
# again only once it has it (set -e)
sh "$here/demo-admin.sh"
systemctl start caddy

echo "== done"
/usr/local/bin/qoqi version
systemctl is-active caddy qoqi-demo
