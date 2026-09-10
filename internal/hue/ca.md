# Embedded Hue trust anchors

`ca.pem` contains Philips Hue `root-bridge` (2017–2038) and Signify Hue
`Hue Root CA 01` (2025–2050), including support for newer bridges.

Publisher: https://developers.meethue.com/develop/application-design-guidance/using-https/

Retrieved 2026-09-08 from the public copy maintained by OpenHue:
https://github.com/openhue/openhue-go/blob/main/certs.go
(The publisher's documentation requires an account.)

The certificates are public trust anchors, not credentials. No system roots or
user-supplied trust anchors are used by the production client. Hostname matching
is intentionally omitted; chain, validity, and server-auth usage are verified.
