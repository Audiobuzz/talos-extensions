#!/usr/bin/env bash
# Ring-2 proof without network privileges: hostapd's built-in RADIUS + EAP-TLS
# server on loopback, eapol_test (hostap's RADIUS-side supplicant) as the
# client. The client key lives in swtpm, reached by wpa_supplicant's OpenSSL
# through pkcs11-provider -> pkcs11-tpm.so. Success == a real EAP-TLS
# handshake with the private-key operation performed in the (software) TPM.
#
# Usage: eapol-tls-test.sh <hostap build dir> <pkcs11-tpm.so> [pkcs11 provider .so]
set -euo pipefail
HOSTAP=$(realpath "$1"); MODULE=$(realpath "$2")
PROVIDER=${3:-$({ ls /usr/lib/*/ossl-modules/pkcs11.so /usr/lib64/ossl-modules/pkcs11.so /usr/lib/ossl-modules/pkcs11.so 2>/dev/null || true; } | head -1)}
EAPOL_TEST=${EAPOL_TEST:-$HOSTAP/eapol_test}
HOSTAPD=${HOSTAPD:-hostapd}
W=$(mktemp -d); PORT=${SWTPM_PORT:-2351}; RPORT=${RADIUS_PORT:-18120}
trap 'kill ${SWTPM_PID:-} ${HOSTAPD_PID:-} 2>/dev/null || true; rm -rf "$W"' EXIT
cd "$W"

echo "== test CA, RADIUS server cert"
openssl ecparam -name prime256v1 -genkey -noout -out ca.key
openssl req -new -x509 -key ca.key -subj /CN=test-ca -days 1 -out ca.pem
openssl ecparam -name prime256v1 -genkey -noout -out server.key
openssl req -new -key server.key -subj /CN=radius -out server.csr
openssl x509 -req -in server.csr -CA ca.pem -CAkey ca.key -CAcreateserial -days 1 -out server.pem 2>/dev/null

echo "== swtpm: ECC key at 0x81000100, CA-signed client cert in NV 0x01800100"
mkdir state
swtpm socket --tpm2 --tpmstate dir="$W/state" --server type=tcp,port=$PORT,bindaddr=127.0.0.1 \
  --ctrl type=tcp,port=$((PORT+1)),bindaddr=127.0.0.1 --flags not-need-init,startup-clear &
SWTPM_PID=$!; sleep 0.5
export TPM2TOOLS_TCTI="swtpm:host=127.0.0.1,port=$PORT"
tpm2_createprimary -C o -G ecc256:null -g sha256 -c key.ctx -a 'fixedtpm|fixedparent|sensitivedataorigin|userwithauth|sign|noda' -Q
tpm2_evictcontrol -C o -c key.ctx 0x81000100 -Q
tpm2_readpublic -c 0x81000100 -f pem -o key.pub -Q
openssl req -new -key ca.key -subj /CN=host -out client.csr
openssl x509 -req -in client.csr -CA ca.pem -CAkey ca.key -force_pubkey key.pub -days 1 -set_serial 11 -out client.pem 2>/dev/null
openssl x509 -in client.pem -outform DER -out client.der
tpm2_nvdefine 0x01800100 -C o -s "$(stat -c %s client.der)" -a 'ownerwrite|ownerread|authwrite|authread' -Q
tpm2_nvwrite 0x01800100 -C o -i client.der -Q

# Any hostap binary built without CONFIG_SMARTCARD loads the OpenSSL pkcs11
# provider by name; without this preference OpenSSL routes its own software
# key operations to the (absent or read-only) token. The extension ships the
# same file; the host-side hostapd needs it too.
cat > openssl.cnf <<CNF
openssl_conf = openssl_init
[openssl_init]
alg_section = algs
[algs]
default_properties = ?provider=default
CNF
export OPENSSL_CONF="$W/openssl.cnf"

echo "== hostapd as RADIUS/EAP-TLS server on 127.0.0.1:$RPORT"
cat > hostapd.conf <<CFG
interface=lo
driver=none
ssid=eaptest
logger_stdout=-1
logger_stdout_level=2
ctrl_interface=$W/hostapd-ctrl
eap_server=1
eap_user_file=$W/eap_user
ca_cert=$W/ca.pem
server_cert=$W/server.pem
private_key=$W/server.key
radius_server_clients=$W/radius_clients
radius_server_auth_port=$RPORT
CFG
echo '* TLS' > eap_user
echo '127.0.0.1/32 testing123' > radius_clients
$HOSTAPD hostapd.conf > hostapd.log 2>&1 &
HOSTAPD_PID=$!; sleep 1
kill -0 $HOSTAPD_PID || { cat hostapd.log; exit 1; }

echo "== eapol_test: EAP-TLS with private_key=pkcs11: URI (provider -> pkcs11-tpm -> swtpm)"
cat > supplicant.conf <<CFG
network={
    key_mgmt=IEEE8021X
    eap=TLS
    identity="host"
    ca_cert="$W/ca.pem"
    client_cert="pkcs11:token=tpm;object=cert;type=cert"
    private_key="pkcs11:token=tpm;object=key;type=private"
}
CFG
export PKCS11_TPM_DEVICE="tcp://127.0.0.1:$PORT" PKCS11_TPM_KEY_HANDLE=0x81000100 PKCS11_TPM_CERT_NV=0x01800100 PKCS11_TPM_DEBUG=1
OPENSSL_MODULES="$(dirname "$PROVIDER")"
export PKCS11_PROVIDER_MODULE="$MODULE" OPENSSL_MODULES
set +e
"$EAPOL_TEST" -c supplicant.conf -a 127.0.0.1 -p "$RPORT" -s testing123 -n -t 10 > eapol.log 2>&1
rc=$?
set -e
grep -E "pkcs11-tpm:|EAP: Status|CTRL-EVENT-EAP|OpenSSL: .*(provider|Failed|error)|SUCCESS|FAILURE" eapol.log | tail -15
if [ $rc -eq 0 ] && grep -q '^SUCCESS' eapol.log; then
  echo "== EAP-TLS SUCCESS: TPM-held key authenticated via PKCS#11"
else
  echo "== EAP-TLS FAILED (rc=$rc); client TLS/provider lines:"
  grep -iE "openssl|pkcs11|provider|store|private key|client cert|tls:|engine|SSL:" eapol.log | grep -vE "TLS: Received|TLS: Pad" | tail -40
  echo "-- hostapd:"; tail -8 hostapd.log
  cp eapol.log hostapd.log "${KEEP_LOGS:-/tmp}/" 2>/dev/null || true
  exit 1
fi
