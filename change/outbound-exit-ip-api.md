#### 1.14.0

* Add a loopback-only Clash API endpoint at `GET /proxies/{name}/exit-ip?ip_version=4|6` for Ackwrap. The endpoint reuses configured Clash API authentication, requests a fixed Cloudflare Trace address directly through the named outbound, and does not change selector state.
