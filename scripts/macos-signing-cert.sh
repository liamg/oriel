#!/bin/sh
# Makes the self-signed code-signing identity release.yml signs Oriel.app with: a
# 20-year certificate named "Oriel Self-Signed" and its key, as a password-protected .p12. Run it once and keep the
# .p12 and its password somewhere safe: a new certificate is a new identity, so macOS asks again for every privacy
# and keychain grant.
#
#	scripts/macos-signing-cert.sh [OUT_DIR]
set -eu

name="Oriel Self-Signed"
out=${1:-.}
p12="$out/oriel-signing.p12"

command -v openssl >/dev/null || {
	echo "macos-signing-cert.sh: needs openssl" >&2
	exit 1
}
if [ -e "$p12" ]; then
	echo "macos-signing-cert.sh: $p12 exists; move it away first" >&2
	exit 1
fi
mkdir -p "$out"

printf 'Password for the .p12 (MACOS_SIGNING_PASSWORD): '
stty -echo 2>/dev/null || true
read -r password
stty echo 2>/dev/null || true
echo
if [ -z "$password" ]; then
	echo "macos-signing-cert.sh: the .p12 needs a password" >&2
	exit 1
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cat >"$work/cert.cnf" <<CNF
[req]
distinguished_name = dn
prompt = no
x509_extensions = ext
[dn]
CN = $name
[ext]
basicConstraints = critical, CA:false
keyUsage = critical, digitalSignature
extendedKeyUsage = critical, codeSigning
subjectKeyIdentifier = hash
CNF

openssl req -x509 -newkey rsa:3072 -nodes -sha256 -days 7300 -config "$work/cert.cnf" \
	-keyout "$work/key.pem" -out "$work/cert.pem" 2>/dev/null
# macOS's security import reads the older PKCS#12 ciphers, not OpenSSL 3's defaults.
PASSWORD=$password openssl pkcs12 -export -name "$name" -inkey "$work/key.pem" -in "$work/cert.pem" \
	-keypbe PBE-SHA1-3DES -certpbe PBE-SHA1-3DES -macalg sha1 -passout env:PASSWORD -out "$p12"
chmod 600 "$p12"

echo "Made $p12: \"$name\", valid until $(openssl x509 -in "$work/cert.pem" -noout -enddate | cut -d= -f2)."
echo
echo "Set the repository secret MACOS_SIGNING_P12 to:"
echo
base64 <"$p12" | tr -d '\n'
echo
echo
echo "and the secret MACOS_SIGNING_PASSWORD to the password you just typed, e.g.:"
printf '  base64 < %s | tr -d %s | gh secret set MACOS_SIGNING_P12\n' "$p12" "'\\n'"
echo "  gh secret set MACOS_SIGNING_PASSWORD"
echo
echo "To sign locally, import the .p12 into your login keychain (open $p12), then:"
echo "  make app CODESIGN_IDENTITY=\"$name\""
