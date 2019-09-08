## Motivation

Save WAN bandwidth and probably speed up repeated downloads by caching packages for LAN computers.

## Example usage as an Arch Linux mirror

Install:

```
go get github.com/afq984/cachingreverseproxy
```

Run:

```
cachingreverseproxy --upstream=http://archlinux.cs.nctu.edu.tw --port=8000
```

Configure the `mirrorlist`

```
# /etc/pacman.d/mirrorlist

Server = http://192.168.200.1:8000/$repo/os/$arch
```

where `192.168.200.1:8000` is the address / port the proxy is running on

## Notes

*   The proxy starts responding to client requests as soon as the upstream response is available, so the proxy would not make the download slower.
*   `If-Modified-Since` is used to validate the cache with the upstream server. Valid if upstream responded `304`, invalid otherwise.
*   Only upstream `200` responses, with `Content-Length`, `Last-Modified`, `Accept-Ranges: bytes` headers are cached.
*   Responses that are not `200` are usually errors so they are not cached.
*   Responses without the headers mentioned above are usually directory listings so are not cached as well.
*   Redirects are followed by the proxy itself and not passed down to the client.
*   Only `Content-Length`, `Last-Modified`, `Accept-Ranges`, `Content-Type` are passed to the downstream client. Other headers are removed from the proxy.
*   Only `HEAD` and `GET` requests.
