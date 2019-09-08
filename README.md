## Motivation

Save WAN bandwidth by caching packages for LAN computers

## Example usage as an Arch Linux mirror

Run:

```
cachingreverseproxy --upstream=http://archlinux.cs.nctu.edu.tw --port=8000
```

And configure the ``mirrorlist``

```
# /etc/pacman.d/mirrorlist

Server = http://192.168.200.1:8000/$repo/os/$arch
```

where ``192.168.200.1:8000`` is the address / port the proxy is running on
