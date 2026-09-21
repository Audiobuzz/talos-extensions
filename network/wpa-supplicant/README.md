# wpa-supplicant

Wired IEEE 802.1X (EAP) and MACsec (MKA) for Talos Linux, as a system
extension. It packages [wpa_supplicant](https://w1.fi/wpa_supplicant/) built
for the `wired` and `macsec_linux` drivers only, OpenSSL's
[pkcs11-provider](https://github.com/latchset/pkcs11-provider), and
[pkcs11-tpm](https://github.com/audiobuzz/pkcs11-tpm) so the client
identity can be a key held in the machine's TPM 2.0. No Wi-Fi code is
included.

The service starts before the machine configuration is fetched, so a node
can authenticate its switch port first and then download `talos.config=`
over the authenticated (and, with MACsec, encrypted) link.

## Installation

Custom extensions are not available through Image Factory. Build boot
assets with `imager`:

```sh
docker run --rm -t -v $PWD/_out:/out ghcr.io/siderolabs/imager:v1.14.1 iso \
  --system-extension-image ghcr.io/audiobuzz/wpa-supplicant:<version>@sha256:<digest>
```

## Configuration

Everything is set through one `ExtensionServiceConfig` document. The
supplicant configuration is a normal `wpa_supplicant.conf`; nothing about it
is Talos specific.

```yaml
apiVersion: v1alpha1
kind: ExtensionServiceConfig
name: wpa-supplicant
environment:
  - WPA_INTERFACE=uplink          # interface name or a Talos link alias
  - WPA_DRIVER=macsec_linux       # or "wired" for 802.1X without MACsec
  - WPA_MODULES=macsec            # optional, loaded before start (see below)
  - PKCS11_TPM_KEY_HANDLE=0x81000100
  - PKCS11_TPM_CERT_NV=0x01800100
configFiles:
  - mountPath: /etc/wpa_supplicant/wpa_supplicant.conf
    content: |
      eapol_version=3
      ap_scan=0
      network={
          key_mgmt=IEEE8021X
          eap=TLS
          identity="host"
          ca_cert="/etc/wpa_supplicant/ca.pem"
          client_cert="pkcs11:token=tpm;object=cert;type=cert"
          private_key="pkcs11:token=tpm;object=key;type=private"
          macsec_policy=1
          macsec_integ_only=0
          macsec_replay_protect=1
      }
  - mountPath: /etc/wpa_supplicant/ca.pem
    content: |
      -----BEGIN CERTIFICATE-----
      ...
```

| Variable | Default | Meaning |
|---|---|---|
| `WPA_INTERFACE` | required | Interface to authenticate on. |
| `WPA_DRIVER` | `macsec_linux` | `macsec_linux` or `wired`. |
| `WPA_CONFIG` | `/etc/wpa_supplicant/wpa_supplicant.conf` | Configuration file path. |
| `WPA_MODULES` | unset | Comma-separated kernel modules to load first, best effort. |
| `WPA_DEBUG` | unset | Verbose wpa_supplicant logging. |
| `WPA_EXTRA_ARGS` | unset | Extra wpa_supplicant arguments. |
| `PKCS11_TPM_*` | see pkcs11-tpm | TPM key handle, certificate NV index, device, labels. |
| `PKCS11_PROVIDER_MODULE` | `/usr/local/lib/pkcs11-tpm.so` | Use another PKCS#11 module instead. |

Keys in files, PEAP or TTLS with passwords, and MKA pre-shared keys
(`mka_cak`/`mka_ckn`) all work too; they are plain wpa_supplicant features.

### Network documents that go with it

With MACsec, IP must ride on `macsec0`, which wpa_supplicant creates, and
Talos must leave the physical link alone. Declaring any new-style network
document turns off Talos's default DHCP-on-every-link behaviour, so:

```yaml
apiVersion: v1alpha1
kind: LinkAliasConfig
name: uplink
selector:
  match: link.physical && link.driver == "ice"
---
apiVersion: v1alpha1
kind: LinkConfig
name: uplink
up: true
mtu: 1532            # MACsec adds 32 bytes
---
apiVersion: v1alpha1
kind: DHCPv4Config
name: macsec0
---
apiVersion: v1alpha1
kind: KernelModuleConfig
name: macsec
```

Talos does not auto-load kernel modules (`CONFIG_MODPROBE_PATH` is empty);
`KernelModuleConfig` is the supported way to load `macsec`, `WPA_MODULES`
is a best-effort fallback for the first boot. `macsec.ko` requires a Talos
kernel built with `CONFIG_MACSEC` (siderolabs/pkgs PR #1694); without it use
`WPA_DRIVER=wired` and `macsec_policy=0` for 802.1X without link encryption.

### First boot

Put the `ExtensionServiceConfig` and the network documents in
`talos.config.early=` (zstd-compressed, base64) on the kernel command line.
They apply before the `talos.config=` URL is fetched, so the very first boot
authenticates the port too. After installation the STATE partition holds
them.

## Status for other services

While the port is `Authorized`, the entrypoint keeps
`/run/wpa_supplicant/<interface>.authorized` present. Another extension
service can gate on it:

```yaml
depends:
  - path: /run/wpa_supplicant/uplink.authorized
```

## Key requirements

The TPM key must be a non-restricted signing key at a persistent handle,
ideally ECC P-256 created with a NULL scheme. Enrolling such a key and
writing its certificate to NV is deliberately outside this extension.

## Why the OpenSSL config

`/etc/ssl/openssl.cnf` sets `default_properties = ?provider=default`.
wpa_supplicant loads the pkcs11 provider by name before OpenSSL's default
provider is activated, and OpenSSL would then generate the TLS handshake's
ephemeral ECDHE key on the read-only token and fail. The preference keeps
software operations in software; signatures with the token key still go to
the TPM because the key object belongs to the provider.
