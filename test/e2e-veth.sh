#!/usr/bin/env bash
# End-to-end test of the BUILT extension image, no Talos required:
#  - hostapd (wired driver, built-in EAP-TLS server, MACsec optional) on one
#    end of a veth pair,
#  - the extension's container rootfs run as a plain container with host
#    networking on the other end, driven by wpa-supplicant-boot exactly as
#    Talos would run it,
#  - the client key in swtpm, reached via pkcs11-provider -> pkcs11-tpm.so.
# Pass: the boot entrypoint publishes /run/wpa_supplicant/<iface>.authorized
# and hostapd reports EAP success (and, with MACSEC=1, a macsec0 link exists).
#
# Usage: sudo test/e2e-veth.sh <container rootfs dir> [MACSEC=0|1]
# Needs: docker, swtpm, tpm2-tools, hostapd (wired driver), iproute2.
set -euo pipefail
ROOTFS=$(realpath "$1"); MACSEC=${MACSEC:-${2:-0}}
W=$(mktemp -d); PORT=${SWTPM_PORT:-2361}; IMG=wpa-supplicant-e2e:$$
AP=veth-ap; SUP=veth-sup
cleanup() {
  docker rm -f wpa-e2e >/dev/null 2>&1 || true
  kill ${HOSTAPD_PID:-} ${SWTPM_PID:-} 2>/dev/null || true
  ip link del $AP 2>/dev/null || true
  docker rmi -f "$IMG" >/dev/null 2>&1 || true
  rm -rf "$W" /run/wpa_supplicant
}
trap cleanup EXIT
cd "$W"

echo "== image from the extension container rootfs"
tar -C "$ROOTFS" -c . | docker import - "$IMG" >/dev/null

echo "== CA, server cert, swtpm key + NV cert"
openssl ecparam -name prime256v1 -genkey -noout -out ca.key
openssl req -new -x509 -key ca.key -subj /CN=test-ca -days 1 -out ca.pem
openssl ecparam -name prime256v1 -genkey -noout -out server.key
openssl req -new -key server.key -subj /CN=radius -out server.csr
openssl x509 -req -in server.csr -CA ca.pem -CAkey ca.key -CAcreateserial -days 1 -out server.pem 2>/dev/null
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

echo "== veth pair + hostapd wired authenticator (MACSEC=$MACSEC)"
ip link add $AP type veth peer name $SUP
ip link set $AP up; ip link set $SUP up
cat > hostapd.conf <<CFG
interface=$AP
driver=wired
logger_stdout=-1
logger_stdout_level=2
ctrl_interface=$W/hostapd-ctrl
ieee8021x=1
eap_server=1
eap_user_file=$W/eap_user
ca_cert=$W/ca.pem
server_cert=$W/server.pem
private_key=$W/server.key
eap_reauth_period=0
CFG
if [ "$MACSEC" = 1 ]; then
  sed -i 's/^driver=wired/driver=macsec_linux/' hostapd.conf
  printf 'macsec_policy=1\nmacsec_integ_only=0\nmacsec_replay_protect=1\n' >> hostapd.conf
fi
echo '* TLS' > eap_user
${HOSTAPD:-hostapd} hostapd.conf > hostapd.log 2>&1 &
HOSTAPD_PID=$!; sleep 1
kill -0 $HOSTAPD_PID || { cat hostapd.log; exit 1; }

echo "== supplicant config (as an ExtensionServiceConfig would mount it)"
cat > wpa_supplicant.conf <<CFG
eapol_version=3
ap_scan=0
network={
    key_mgmt=IEEE8021X
    eap=TLS
    identity="host"
    ca_cert="/etc/wpa_supplicant/ca.pem"
    client_cert="pkcs11:token=tpm;object=cert;type=cert"
    private_key="pkcs11:token=tpm;object=key;type=private"
    macsec_policy=$MACSEC
}
CFG
mkdir -p /run/wpa_supplicant
DRIVER=wired; [ "$MACSEC" = 1 ] && DRIVER=macsec_linux
docker run -d --name wpa-e2e --network host --privileged \
  -v "$W/wpa_supplicant.conf:/etc/wpa_supplicant/wpa_supplicant.conf:ro" \
  -v "$W/ca.pem:/etc/wpa_supplicant/ca.pem:ro" \
  -v /run/wpa_supplicant:/run/wpa_supplicant \
  -e WPA_INTERFACE=$SUP -e WPA_DRIVER=$DRIVER -e WPA_DEBUG=1 \
  -e PKCS11_TPM_DEVICE="tcp://127.0.0.1:$PORT" -e PKCS11_TPM_KEY_HANDLE=0x81000100 -e PKCS11_TPM_CERT_NV=0x01800100 -e PKCS11_TPM_DEBUG=1 \
  -e OPENSSL_MODULES=/usr/lib/ossl-modules -e OPENSSL_CONF=/etc/ssl/openssl.cnf \
  "$IMG" /usr/local/bin/wpa-supplicant-boot >/dev/null

echo "== waiting for the authorized marker"
for _ in $(seq 1 30); do
  [ -e /run/wpa_supplicant/$SUP.authorized ] && break
  sleep 1
done
docker logs wpa-e2e 2>&1 | grep -E "wpa-supplicant-boot:|CTRL-EVENT-EAP|pkcs11-tpm:|MACsec|MKA" | tail -15
if [ ! -e /run/wpa_supplicant/$SUP.authorized ]; then
  echo "== FAILED: port not authorized"; docker logs wpa-e2e 2>&1 | tail -40; echo "-- hostapd:"; tail -20 hostapd.log; exit 1
fi
grep -q "CTRL-EVENT-EAP-SUCCESS" hostapd.log || { echo "== FAILED: hostapd saw no EAP success"; tail -20 hostapd.log; exit 1; }
if [ "$MACSEC" = 1 ]; then
  ip macsec show | grep -q "macsec" || { echo "== FAILED: no macsec link"; ip macsec show; exit 1; }
  echo "-- macsec:"; ip macsec show | head -8
fi
echo "== E2E PASSED: port authorized with the TPM key (MACSEC=$MACSEC)"
