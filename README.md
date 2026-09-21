# talos-extensions

Talos Linux system extensions, built with [bldr](https://github.com/siderolabs/bldr)
the same way [siderolabs/extensions](https://github.com/siderolabs/extensions) is.

| Extension | What |
|---|---|
| [network/wpa-supplicant](network/wpa-supplicant/) | wired 802.1X + MACsec supplicant with a TPM-held identity via PKCS#11 |

## Building

Needs Docker with BuildKit. The `PKGS`/`TOOLS` tags must match the Talos
release the extension targets (`pkg/machinery/gendata/data/{pkgs,tools}` in
the Talos tree at that tag).

```sh
make wpa-supplicant                 # build for linux/amd64,linux/arm64 (cache only)
make local-wpa-supplicant           # export the image tree to _out/
make wpa-supplicant PUSH=true       # push to $REGISTRY/$USERNAME/wpa-supplicant:<version>
```

## Testing without Talos

`test/eapol-loopback.sh` proves the key path with a host build of hostap:
hostapd as RADIUS/EAP-TLS server on loopback, `eapol_test` as client, the
client key in swtpm behind pkcs11-tpm. No network privileges needed.

`test/e2e-veth.sh` runs the built extension rootfs as a container on one end
of a veth pair against a hostapd wired authenticator, exactly as
Talos would run it, including MACsec with `MACSEC=1`. Needs root and Docker.

## License

Apache License 2.0.
