package configgen

const proxyGroupsBase = `
proxy-groups:
  - {name: 🚀 节点选择, type: select, proxies: [♻️ 自动选择], include-all: true}
  - {name: ♻️ 自动选择, type: url-test, include-all: true, tolerance: 100}
  - {name: 🐟 漏网之鱼, type: select, proxies: [🚀 节点选择, 🎯 全球直连]}
  - {name: 🎯 全球直连, type: select, proxies: [DIRECT], hidden: true}
`
