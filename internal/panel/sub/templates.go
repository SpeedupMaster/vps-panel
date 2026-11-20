package sub

// DefaultMihomo: anti-DNS-leak DNS config, dynamic country groups (auto-updated
// from current nodes, with AI avoiding HK/CN/MO/RU via {{regions_ai}}), and
// comprehensive routing rules. Generated groups (节点选择 / 自动选择 / 故障转移 /
// 各国家测速组) are inserted by Mihomo().
const DefaultMihomo = `mixed-port: 7890
allow-lan: false
bind-address: "*"
mode: rule
log-level: warning
# 关闭 IPv6 可彻底避免本地双栈环境下的 IPv6 泄露；
# 如所有落地 VPS 均支持 IPv6 且有访问需求，请将此处与下方 dns.ipv6 同步改为 true
ipv6: false
unified-delay: true
tcp-concurrent: true
find-process-mode: strict
keep-alive-idle: 600
keep-alive-interval: 15
geodata-mode: true
geo-auto-update: true
geo-update-interval: 48
geox-url:
  geoip: https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat
  geosite: https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat
  mmdb: https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/country.mmdb
  asn: https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/GeoLite2-ASN.mmdb
profile:
  store-selected: true
  store-fake-ip: true
sniffer:
  enable: true
  force-dns-mapping: true
  parse-pure-ip: true
  override-destination: true
  sniff:
    HTTP: {ports: [80, 8080-8880], override-destination: true}
    TLS: {ports: [443, 8443]}
    QUIC: {ports: [443, 8443]}
  skip-domain:
    - "Mijia Cloud"
    - "+.push.apple.com"
    - "dlg.io.mi.com"
tun:
  enable: false
  stack: mixed
  auto-route: true
  auto-detect-interface: true
  strict-route: true
  dns-hijack:
    - "any:53"
    - "tcp://any:53"
dns:
  enable: true
  cache-algorithm: arc
  ipv6: false
  use-hosts: true
  use-system-hosts: true
  respect-rules: true
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  fake-ip-filter-mode: blacklist
  fake-ip-filter:
    - "+.lan"
    - "+.local"
    - "+.localhost"
    - "localhost.ptlogin2.qq.com"
    - "localhost.sec.qq.com"
    - "+.msftconnecttest.com"
    - "+.msftncsi.com"
    - "captive.apple.com"
    - "time.*.com"
    - "time.*.gov"
    - "time.*.edu.cn"
    - "time.*.apple.com"
    - "ntp.*.com"
    - "+.pool.ntp.org"
    - "+.stun.*.*"
    - "+.stun.*.*.*"
    - "+.stun.*.*.*.*"
    - "+.xboxlive.com"
    - "+.nintendo.net"
    - "+.playstation.net"
    - "+.battlenet.com.cn"
    - "+.wotgame.cn"
    - "+.wggames.cn"
    - "+.wowsgame.cn"
    - "+.wargaming.net"
    - "proxy.golang.org"
    - "+.market.xiaomi.com"
    - "geosite:private"
    - "geosite:cn"
  default-nameserver:
    - 223.5.5.5
    - 119.29.29.29
  proxy-server-nameserver:
    - https://223.5.5.5/dns-query
    - https://1.12.12.12/dns-query
  direct-nameserver:
    - https://223.5.5.5/dns-query
    - https://1.12.12.12/dns-query
  direct-nameserver-follow-policy: false
  nameserver:
    - https://1.1.1.1/dns-query
    - https://8.8.8.8/dns-query
  nameserver-policy:
    "geosite:private,cn":
      - https://223.5.5.5/dns-query
      - https://1.12.12.12/dns-query
proxy-groups:
  - {name: 🤖 AI 服务, type: select, proxies: ["{{regions_ai}}", 节点选择, "{{regions}}", 故障转移, "{{all}}"]}
  - {name: 🧠 Claude, type: select, proxies: ["{{regions_ai}}", 节点选择, "{{regions}}", 故障转移, "{{all}}"]}
  - {name: 📺 YouTube, type: select, proxies: [节点选择, "{{regions}}", 自动选择, 故障转移, "{{all}}"]}
  - {name: 🎬 国际流媒体, type: select, proxies: [节点选择, "{{regions}}", 自动选择, 故障转移, "{{all}}"]}
  - {name: 💬 社交通讯, type: select, proxies: [节点选择, "{{regions}}", 自动选择, 故障转移, "{{all}}"]}
  - {name: 🔍 Google, type: select, proxies: [节点选择, "{{regions}}", 自动选择, 故障转移, "{{all}}"]}
  - {name: 💻 开发与学术, type: select, proxies: [节点选择, "{{regions}}", 自动选择, 故障转移, "{{all}}", DIRECT]}
  - {name: 🪟 微软服务, type: select, proxies: [节点选择, DIRECT, "{{regions}}", 自动选择]}
  - {name: 🍎 Apple 服务, type: select, proxies: [DIRECT, 节点选择, "{{regions}}"]}
  - {name: 🎮 游戏平台, type: select, proxies: [节点选择, DIRECT, "{{regions}}", "{{all}}"]}
  - {name: 💰 金融与加密, type: select, proxies: [节点选择, "{{regions}}", 故障转移, "{{all}}"]}
  - {name: 📺 国内媒体, type: select, proxies: [DIRECT, "{{regions}}", 节点选择]}
  - {name: 🛡️ 广告拦截, type: select, proxies: [REJECT, DIRECT]}
  - {name: 🌐 漏网之鱼, type: select, proxies: [节点选择, "{{regions}}", 自动选择, 故障转移, DIRECT]}
rules:
  # 1. 本地与局域网直连
  - GEOSITE,private,DIRECT
  - GEOIP,private,DIRECT,no-resolve
  # 2. 上游 DoH 服务器显式路由（配合 respect-rules: true）
  - IP-CIDR,223.5.5.5/32,DIRECT,no-resolve
  - IP-CIDR,223.6.6.6/32,DIRECT,no-resolve
  - IP-CIDR,1.12.12.12/32,DIRECT,no-resolve
  - IP-CIDR,120.53.53.53/32,DIRECT,no-resolve
  - IP-CIDR,119.29.29.29/32,DIRECT,no-resolve
  - IP-CIDR,1.1.1.1/32,节点选择,no-resolve
  - IP-CIDR,1.0.0.1/32,节点选择,no-resolve
  - IP-CIDR,8.8.8.8/32,节点选择,no-resolve
  - IP-CIDR,8.8.4.4/32,节点选择,no-resolve
  # 3. 广告与追踪拦截
  - GEOSITE,category-ads-all,🛡️ 广告拦截
  # 4. Claude（置于 AI 服务之前，避免被 category-ai-!cn 提前命中）
  - GEOSITE,anthropic,🧠 Claude
  - DOMAIN-SUFFIX,anthropic.com,🧠 Claude
  - DOMAIN-SUFFIX,claude.ai,🧠 Claude
  - DOMAIN-SUFFIX,claude.com,🧠 Claude
  - DOMAIN-SUFFIX,claudeusercontent.com,🧠 Claude
  - DOMAIN-SUFFIX,clau.de,🧠 Claude
  # 5. AI 服务（置于 Google / Microsoft / GitHub 之前）
  - GEOSITE,category-ai-!cn,🤖 AI 服务
  - GEOSITE,openai,🤖 AI 服务
  - GEOSITE,google-gemini,🤖 AI 服务
  - DOMAIN-SUFFIX,openai.com,🤖 AI 服务
  - DOMAIN-SUFFIX,chatgpt.com,🤖 AI 服务
  - DOMAIN-SUFFIX,oaistatic.com,🤖 AI 服务
  - DOMAIN-SUFFIX,oaiusercontent.com,🤖 AI 服务
  - DOMAIN-SUFFIX,gemini.google.com,🤖 AI 服务
  - DOMAIN-SUFFIX,aistudio.google.com,🤖 AI 服务
  - DOMAIN-SUFFIX,generativelanguage.googleapis.com,🤖 AI 服务
  - DOMAIN-SUFFIX,deepmind.com,🤖 AI 服务
  - DOMAIN-SUFFIX,deepmind.google,🤖 AI 服务
  - DOMAIN-SUFFIX,perplexity.ai,🤖 AI 服务
  - DOMAIN-SUFFIX,x.ai,🤖 AI 服务
  - DOMAIN-SUFFIX,grok.com,🤖 AI 服务
  - DOMAIN-SUFFIX,poe.com,🤖 AI 服务
  - DOMAIN-SUFFIX,cursor.sh,🤖 AI 服务
  - DOMAIN-SUFFIX,cursor.com,🤖 AI 服务
  - DOMAIN-SUFFIX,cursor-cdn.com,🤖 AI 服务
  - DOMAIN-SUFFIX,copilot.microsoft.com,🤖 AI 服务
  - DOMAIN-SUFFIX,githubcopilot.com,🤖 AI 服务
  - DOMAIN-SUFFIX,openrouter.ai,🤖 AI 服务
  - DOMAIN-SUFFIX,groq.com,🤖 AI 服务
  - DOMAIN-SUFFIX,huggingface.co,🤖 AI 服务
  # 6. YouTube（置于 Google 之前）
  - GEOSITE,youtube,📺 YouTube
  # 7. 国际流媒体
  - GEOSITE,netflix,🎬 国际流媒体
  - GEOIP,netflix,🎬 国际流媒体,no-resolve
  - GEOSITE,disney,🎬 国际流媒体
  - GEOSITE,tiktok,🎬 国际流媒体
  - GEOSITE,spotify,🎬 国际流媒体
  - GEOSITE,hbo,🎬 国际流媒体
  - GEOSITE,primevideo,🎬 国际流媒体
  - GEOSITE,bahamut,🎬 国际流媒体
  - GEOSITE,bilibili@!cn,🎬 国际流媒体
  - DOMAIN-SUFFIX,biliintl.com,🎬 国际流媒体
  # 8. 国内媒体（置于 cn 之前，支持手动切港澳台番剧）
  - GEOSITE,bilibili,📺 国内媒体
  # 9. 社交通讯
  - GEOSITE,telegram,💬 社交通讯
  - GEOIP,telegram,💬 社交通讯,no-resolve
  - GEOSITE,discord,💬 社交通讯
  - GEOSITE,twitter,💬 社交通讯
  - GEOIP,twitter,💬 社交通讯,no-resolve
  - GEOSITE,facebook,💬 社交通讯
  - GEOIP,facebook,💬 社交通讯,no-resolve
  - GEOSITE,instagram,💬 社交通讯
  - GEOSITE,whatsapp,💬 社交通讯
  - GEOSITE,reddit,💬 社交通讯
  # 10. 开发与海外学术（GitHub 置于微软之前）
  - GEOSITE,github,💻 开发与学术
  - GEOSITE,category-dev,💻 开发与学术
  - GEOSITE,category-scholar-!cn,💻 开发与学术
  # 11. Google 服务
  - GEOSITE,google,🔍 Google
  - GEOIP,google,🔍 Google,no-resolve
  # 12. Apple 服务
  - GEOSITE,apple-cn,DIRECT
  - GEOSITE,icloud,🍎 Apple 服务
  - GEOSITE,apple,🍎 Apple 服务
  # 13. 微软服务
  - GEOSITE,microsoft@cn,DIRECT
  - GEOSITE,onedrive,🪟 微软服务
  - GEOSITE,microsoft,🪟 微软服务
  # 14. 游戏平台
  - GEOSITE,steam@cn,DIRECT
  - GEOSITE,category-games@cn,DIRECT
  - GEOSITE,steam,🎮 游戏平台
  - GEOSITE,category-games,🎮 游戏平台
  # 15. 金融与加密资产
  - GEOSITE,paypal,💰 金融与加密
  - GEOSITE,category-cryptocurrency,💰 金融与加密
  # 16. 其他境外常见域名
  - GEOSITE,geolocation-!cn,节点选择
  # 17. 中国大陆直连
  - GEOSITE,cn,DIRECT
  - DOMAIN-SUFFIX,cn,DIRECT
  - GEOIP,CN,DIRECT,no-resolve
  # 18. 兜底规则
  - MATCH,🌐 漏网之鱼
`

// DefaultSingBox targets sing-box 1.12+ (new DNS server format, rule actions)
// with anti-DNS-leak defaults, dynamic regions, and matching split routing.
const DefaultSingBox = `{
  "log": {"level": "warn", "timestamp": true},
  "dns": {
    "servers": [
      {"type": "https", "tag": "remote", "server": "1.1.1.1", "detour": "节点选择"},
      {"type": "https", "tag": "local", "server": "223.5.5.5"},
      {"type": "fakeip", "tag": "fakeip", "inet4_range": "198.18.0.0/15", "inet6_range": "fc00::/18"}
    ],
    "rules": [
      {"rule_set": ["geosite-private", "geosite-cn"], "server": "local"},
      {"query_type": ["A", "AAAA"], "server": "fakeip"}
    ],
    "final": "remote",
    "strategy": "ipv4_only",
    "independent_cache": true
  },
  "inbounds": [
    {"type": "tun", "tag": "tun-in", "address": ["172.19.0.1/30"], "auto_route": true, "strict_route": true},
    {"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 2080}
  ],
  "outbounds": [
    {"type": "selector", "tag": "🤖 AI 服务", "outbounds": ["{{regions_ai}}", "节点选择", "{{regions}}", "{{all}}"]},
    {"type": "selector", "tag": "🧠 Claude", "outbounds": ["{{regions_ai}}", "节点选择", "{{regions}}", "{{all}}"]},
    {"type": "selector", "tag": "📺 YouTube", "outbounds": ["节点选择", "{{regions}}", "自动选择", "{{all}}"]},
    {"type": "selector", "tag": "🎬 国际流媒体", "outbounds": ["节点选择", "{{regions}}", "自动选择", "{{all}}"]},
    {"type": "selector", "tag": "💬 社交通讯", "outbounds": ["节点选择", "{{regions}}", "自动选择", "{{all}}"]},
    {"type": "selector", "tag": "🔍 Google", "outbounds": ["节点选择", "{{regions}}", "自动选择", "{{all}}"]},
    {"type": "selector", "tag": "💻 开发与学术", "outbounds": ["节点选择", "{{regions}}", "自动选择", "{{all}}", "direct"]},
    {"type": "selector", "tag": "🪟 微软服务", "outbounds": ["节点选择", "direct", "{{regions}}", "自动选择"]},
    {"type": "selector", "tag": "🍎 Apple 服务", "outbounds": ["direct", "节点选择", "{{regions}}"]},
    {"type": "selector", "tag": "🎮 游戏平台", "outbounds": ["节点选择", "direct", "{{regions}}", "{{all}}"]},
    {"type": "selector", "tag": "📺 国内媒体", "outbounds": ["direct", "{{regions}}", "节点选择"]},
    {"type": "selector", "tag": "🌐 漏网之鱼", "outbounds": ["节点选择", "{{regions}}", "自动选择", "direct"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {
    "rules": [
      {"action": "sniff"},
      {"protocol": "dns", "action": "hijack-dns"},
      {"ip_is_private": true, "outbound": "direct"},
      {"rule_set": "geosite-private", "outbound": "direct"},
      {"rule_set": "geosite-category-ads-all", "action": "reject"},
      {"rule_set": "geosite-anthropic", "outbound": "🧠 Claude"},
      {"domain_suffix": ["anthropic.com", "claude.ai", "claude.com", "claudeusercontent.com", "clau.de"], "outbound": "🧠 Claude"},
      {"rule_set": ["geosite-category-ai-!cn", "geosite-openai", "geosite-google-gemini"], "outbound": "🤖 AI 服务"},
      {"domain_suffix": ["openai.com", "chatgpt.com", "perplexity.ai", "x.ai", "grok.com", "poe.com", "cursor.sh", "cursor.com", "copilot.microsoft.com", "githubcopilot.com", "openrouter.ai"], "outbound": "🤖 AI 服务"},
      {"rule_set": "geosite-youtube", "outbound": "📺 YouTube"},
      {"rule_set": ["geosite-netflix", "geoip-netflix", "geosite-disney", "geosite-tiktok", "geosite-spotify", "geosite-bahamut", "geosite-bilibili@!cn"], "outbound": "🎬 国际流媒体"},
      {"domain_suffix": ["biliintl.com"], "outbound": "🎬 国际流媒体"},
      {"rule_set": "geosite-bilibili", "outbound": "📺 国内媒体"},
      {"rule_set": ["geosite-telegram", "geoip-telegram", "geosite-discord", "geosite-twitter", "geosite-facebook"], "outbound": "💬 社交通讯"},
      {"rule_set": ["geosite-github", "geosite-category-dev", "geosite-category-scholar-!cn"], "outbound": "💻 开发与学术"},
      {"rule_set": ["geosite-google", "geoip-google"], "outbound": "🔍 Google"},
      {"rule_set": "geosite-apple@cn", "outbound": "direct"},
      {"rule_set": "geosite-apple", "outbound": "🍎 Apple 服务"},
      {"rule_set": "geosite-microsoft@cn", "outbound": "direct"},
      {"rule_set": "geosite-microsoft", "outbound": "🪟 微软服务"},
      {"rule_set": ["geosite-steam@cn", "geosite-category-games@cn"], "outbound": "direct"},
      {"rule_set": "geosite-category-games", "outbound": "🎮 游戏平台"},
      {"rule_set": "geosite-geolocation-!cn", "outbound": "节点选择"},
      {"rule_set": ["geosite-cn", "geoip-cn"], "outbound": "direct"}
    ],
    "rule_set": [
      {"type": "remote", "tag": "geosite-private", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-private.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-cn", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-cn.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geoip-cn", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-geolocation-!cn", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-geolocation-!cn.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-category-ads-all", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-ads-all.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-anthropic", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-anthropic.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-category-ai-!cn", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-ai-!cn.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-openai", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-openai.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-google-gemini", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-google-gemini.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-youtube", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-youtube.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-netflix", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-netflix.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geoip-netflix", "format": "binary", "url": "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geoip/netflix.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-disney", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-disney.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-tiktok", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-tiktok.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-spotify", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-spotify.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-bahamut", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-bahamut.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-bilibili@!cn", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-bilibili@!cn.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-bilibili", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-bilibili.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-telegram", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-telegram.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geoip-telegram", "format": "binary", "url": "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geoip/telegram.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-discord", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-discord.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-twitter", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-twitter.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-facebook", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-facebook.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-github", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-github.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-category-dev", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-dev.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-category-scholar-!cn", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-scholar-!cn.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-google", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-google.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geoip-google", "format": "binary", "url": "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geoip/google.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-apple@cn", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-apple@cn.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-apple", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-apple.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-microsoft@cn", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-microsoft@cn.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-microsoft", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-microsoft.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-steam@cn", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-steam@cn.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-category-games@cn", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-games@cn.srs", "download_detour": "节点选择"},
      {"type": "remote", "tag": "geosite-category-games", "format": "binary", "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-games.srs", "download_detour": "节点选择"}
    ],
    "final": "🌐 漏网之鱼",
    "auto_detect_interface": true,
    "default_domain_resolver": "local"
  },
  "experimental": {"cache_file": {"enabled": true, "store_fakeip": true}}
}`
