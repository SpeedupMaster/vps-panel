package sub

// DefaultMihomo: domestic direct, AI and streaming groups. Generated groups
// (节点选择 / 自动选择 / 故障转移 / 地区) are inserted by Mihomo().
const DefaultMihomo = `mixed-port: 7890
allow-lan: false
mode: rule
log-level: warning
ipv6: true
unified-delay: true
tcp-concurrent: true
find-process-mode: strict
global-client-fingerprint: chrome
geodata-mode: true
geo-auto-update: true
geo-update-interval: 72
geox-url:
  geoip: https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat
  geosite: https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat
  mmdb: https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/country.mmdb
profile:
  store-selected: true
  store-fake-ip: true
sniffer:
  enable: true
  sniff:
    HTTP: {ports: [80, 8080-8880], override-destination: true}
    TLS: {ports: [443, 8443]}
    QUIC: {ports: [443, 8443]}
dns:
  enable: true
  ipv6: true
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  fake-ip-filter: ["*.lan", "+.local", "+.msftconnecttest.com", "+.msftncsi.com", "localhost.ptlogin2.qq.com"]
  default-nameserver: [223.5.5.5, 119.29.29.29]
  nameserver: [https://dns.alidns.com/dns-query, https://doh.pub/dns-query]
  proxy-server-nameserver: [https://dns.alidns.com/dns-query, https://doh.pub/dns-query]
  nameserver-policy:
    "geosite:geolocation-!cn": [https://1.1.1.1/dns-query, https://8.8.8.8/dns-query]
proxy-groups:
  - {name: AI, type: select, proxies: ["{{regions}}", 节点选择]}
  - {name: 流媒体, type: select, proxies: [节点选择, "{{regions}}"]}
  - {name: Telegram, type: select, proxies: [节点选择, "{{regions}}"]}
  - {name: 漏网之鱼, type: select, proxies: [节点选择, DIRECT]}
rules:
  - GEOSITE,private,DIRECT
  - GEOSITE,category-ads-all,REJECT
  - GEOSITE,openai,AI
  - GEOSITE,anthropic,AI
  - GEOSITE,google-gemini,AI
  - GEOSITE,netflix,流媒体
  - GEOSITE,disney,流媒体
  - GEOSITE,youtube,流媒体
  - GEOSITE,tiktok,流媒体
  - GEOSITE,telegram,Telegram
  - GEOIP,telegram,Telegram,no-resolve
  - GEOSITE,geolocation-!cn,节点选择
  - GEOSITE,cn,DIRECT
  - GEOIP,private,DIRECT,no-resolve
  - GEOIP,CN,DIRECT,no-resolve
  - MATCH,漏网之鱼
`

// DefaultSingBox targets sing-box 1.12+ (new DNS server format, rule actions).
const DefaultSingBox = `{
  "log": {"level": "warn", "timestamp": true},
  "dns": {
    "servers": [
      {"type": "https", "tag": "remote", "server": "1.1.1.1", "detour": "节点选择"},
      {"type": "https", "tag": "local", "server": "223.5.5.5"},
      {"type": "fakeip", "tag": "fakeip", "inet4_range": "198.18.0.0/15", "inet6_range": "fc00::/18"}
    ],
    "rules": [
      {"rule_set": "geosite-cn", "server": "local"},
      {"query_type": ["A", "AAAA"], "server": "fakeip"}
    ],
    "final": "remote",
    "strategy": "prefer_ipv4"
  },
  "inbounds": [
    {"type": "tun", "tag": "tun-in", "address": ["172.19.0.1/30", "fdfe:dcba:9876::1/126"], "auto_route": true, "strict_route": true},
    {"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 2080}
  ],
  "outbounds": [
    {"type": "selector", "tag": "AI", "outbounds": ["{{regions}}", "节点选择"]},
    {"type": "selector", "tag": "流媒体", "outbounds": ["节点选择", "{{regions}}"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {
    "rules": [
      {"action": "sniff"},
      {"protocol": "dns", "action": "hijack-dns"},
      {"ip_is_private": true, "outbound": "direct"},
      {"rule_set": "geosite-category-ads-all", "action": "reject"},
      {"rule_set": ["geosite-openai", "geosite-anthropic", "geosite-google-gemini"], "outbound": "AI"},
      {"rule_set": ["geosite-netflix", "geosite-youtube", "geosite-disney"], "outbound": "流媒体"},
      {"rule_set": ["geosite-cn", "geoip-cn"], "outbound": "direct"}
    ],
    "rule_set": [
      {"type": "remote", "tag": "geosite-cn", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-cn.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geoip-cn", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-category-ads-all", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-ads-all.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-openai", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-openai.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-anthropic", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-anthropic.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-google-gemini", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-google-gemini.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-netflix", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-netflix.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-youtube", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-youtube.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-disney", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-disney.srs", "download_detour": "节点选择"}
    ],
    "final": "节点选择",
    "auto_detect_interface": true,
    "default_domain_resolver": "local"
  },
  "experimental": {"cache_file": {"enabled": true, "store_fakeip": true}}
}`
