// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import { isLanguageToken } from '@/i18n/dynamicFormText'

/**
 * 存量动态表单的「接入多语言」迁移工具（设计器页专用）。
 *
 * 早期迁移把表单文案写死成中文，没有 options.language。这里把标题/提示/选项
 * 抽成 `{{$t.key}}`：中文进 zh-cn；英文用下面的内置词典一次补全，补不到的留空
 * （运行期会回退中文）。之后就在设计器「多语言」面板里维护，表单再怎么改都不会
 * 丢翻译。
 *
 * **词典只在迁移时使用，运行期不读** —— 译文的事实来源永远是 form_json。
 * 内置表单的迁移已经落在数据库迁移里（见 database/sqlite/migrations/023_*.sql），
 * 这份词典是为了让被定制的存量表单/插件表单也能一键接入。
 */
const legacyTextTranslations = {
  // ---------- 推送通道表单（mqtt / tdengine-v3 / influxdb-v3 / http / opcua） ----------
  'broker地址': 'Broker address',
  端口: 'Port',
  主题: 'Topic',
  qos: 'QoS',
  用户名: 'Username',
  密码: 'Password',
  '启用数据缓存': 'Enable data caching',
  缓存条数: 'Cached batches',
  IP地址: 'IP address',
  数据库名称: 'Database name',
  ssl: 'SSL',
  主机IP: 'Host IP',
  token令牌: 'Token',
  '多个主题使用逗号分割': 'Separate multiple topics with commas',
  '完整的 http/https 地址': 'Full http/https URL',
  '留空使用 application/json': 'Leave empty to use application/json',
  方法: 'Method',
  鉴权方式: 'Auth type',
  无: 'None',
  'Basic 用户名': 'Basic username',
  'Basic 密码': 'Basic password',
  'HMAC 密钥': 'HMAC secret',
  '对原始请求体做 HMAC-SHA256': 'HMAC-SHA256 over the raw request body',
  'HMAC 头名': 'HMAC header name',
  '默认 X-Signature': 'Defaults to X-Signature',
  'HMAC 前缀': 'HMAC prefix',
  '默认 sha256=': 'Defaults to sha256=',
  自定义请求头: 'Custom headers',
  'JSON 对象，如 {"X-Env":"prod"}': 'JSON object, e.g. {"X-Env":"prod"}',
  超时秒数: 'Timeout (seconds)',
  '1 到 120，默认 10': '1 to 120, defaults to 10',
  并发数: 'Concurrency',
  '1 到 64，默认 4': '1 to 64, defaults to 4',
  '跳过 TLS 校验': 'Skip TLS verification',
  '仅自签名证书的内网环境使用': 'Only for internal networks using self-signed certificates',
  监听地址: 'Listen address',
  '0.0.0.0 表示所有网卡；客户端不支持 0.0.0.0 时填本机局域网 IP':
    '0.0.0.0 listens on all interfaces, use the local LAN IP if the client cannot handle 0.0.0.0',
  监听端口: 'Listen port',
  '默认 4840': 'Defaults to 4840',
  '命名空间 URI': 'Namespace URI',
  '默认 urn:iot-gateway:devices': 'Defaults to urn:iot-gateway:devices',
  根节点名称: 'Root node name',
  '地址空间中根 Object 的显示名': 'Display name of the root Object in the address space',
  安全策略: 'Security policy',
  '内网可用 None；加密需同时填证书与私钥':
    'None is fine on an internal network, encryption requires both certificate and private key',
  安全模式: 'Security mode',
  证书文件: 'Certificate file',
  'PEM/DER 证书路径': 'PEM/DER certificate path',
  私钥文件: 'Private key file',
  'PEM 私钥路径（RSA，PKCS#8 / PKCS#1）': 'PEM private key path (RSA, PKCS#8 / PKCS#1)',
  'NodeID 方式': 'NodeID style',
  'id 改名后节点仍稳定；name 可读但改名即失效':
    'id keeps nodes stable after renames, name is readable but breaks on rename',
  '数据库 ID（稳定）': 'Database ID (stable)',
  '设备/点位名（可读）': 'Device/point name (readable)',
  暴露的设备: 'Exposed devices',
  '留空 = 全部启用设备；多个设备 ID 用英文逗号分隔':
    'Empty = all enabled devices, separate multiple device IDs with commas',
  额外公布地址: 'Additional advertised hosts',
  '只用于端点公布、不绑定监听；填 DNS 别名或 NAT 公网 IP，多个用英文逗号分隔':
    'Only advertised in the endpoint, not bound, use DNS aliases or NAT public IPs, separated by commas',

  // ---------- 报警通知表单（dingtalk / wecom / feishu / custom） ----------
  'Webhook地址': 'Webhook URL',
  '钉钉群机器人地址，形如 https://oapi.dingtalk.com/robot/send?access_token=xxx。机器人安全设置选「自定义关键词」时，请把「断联报警」加为关键词':
    'DingTalk group robot URL, e.g. https://oapi.dingtalk.com/robot/send?access_token=xxx. If the robot uses the "custom keyword" security setting, add "断联报警" as a keyword',
  签名密钥: 'Signing secret',
  '机器人安全设置选「加签」时必填（SEC 开头）。未开启加签请留空':
    'Required when the robot uses the "sign" security setting (starts with SEC). Leave empty if signing is off',
  消息格式: 'Message format',
  '默认 markdown': 'Defaults to markdown',
  '@所有人': 'Mention all',
  '@手机号': '@ Phone numbers',
  '多个手机号用英文逗号分隔。留空表示不@具体人':
    'Separate multiple phone numbers with commas. Leave empty to mention nobody',
  '企业微信群机器人地址，形如 https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxx':
    'WeCom group robot URL, e.g. https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxx',
  'markdown 正文上限 4096 字节，text 上限 2048 字节，超出自动截断':
    'markdown body is capped at 4096 bytes, text at 2048 bytes, longer content is truncated',
  '飞书群机器人地址，形如 https://open.feishu.cn/open-apis/bot/v2/hook/xxx':
    'Feishu group robot URL, e.g. https://open.feishu.cn/open-apis/bot/v2/hook/xxx',
  '机器人开启「签名校验」时必填。未开启请留空':
    'Required when the robot has "signature verification" enabled. Leave empty otherwise',
  '默认 text，card 为带标题色块的交互式卡片':
    'Defaults to text, card is an interactive card with a colored title block',
  '@用户open_id': '@ User open_id',
  '多个 open_id 用英文逗号分隔。留空表示不@具体人':
    'Separate multiple open_ids with commas. Leave empty to mention nobody',
  '接收报警 JSON 的任意 HTTP 端点，请求方法为 POST':
    'Any HTTP endpoint that accepts alarm JSON via POST',
  '可选。填写后以 Authorization: Bearer token 形式发送':
    'Optional. When set, sent as Authorization: Bearer token',

  // ---------- 选项标签 ----------
  是: 'Yes',
  否: 'No',
}

const CJK = /[㐀-鿿豈-﫿]/

function legacyEnglish(text) {
  return legacyTextTranslations[text] ?? text
}

/** 需要进多语言表的文案：含中文，或词典里确有不同英文的（如 qos -> QoS）。 */
function needsTranslation(text) {
  return CJK.test(text) || legacyEnglish(text) !== text
}

function sanitizeKey(key) {
  return (
    String(key || 'text')
      .replace(/[^\w-]+/g, '_')
      .replace(/^_+|_+$/g, '') || 'text'
  )
}

/**
 * 把设计器里的中文字面量升级为 `{{$t.key}}` + options.language。
 *
 * 已经是 `{{$t.xxx}}` 的文案原样保留，重复调用是幂等的。
 *
 * @param {Array} rules - 设计器当前规则
 * @param {Object} [language] - 已有的 options.language（会合并，不覆盖其它语言）
 * @returns {{rules: Array, language: Object}}
 */
export function migrateDynamicFormRulesToLanguage(rules, language) {
  const nextLanguage = {}
  Object.keys(language || {}).forEach((lang) => {
    nextLanguage[lang] = { ...(language[lang] || {}) }
  })
  if (!nextLanguage['zh-cn']) nextLanguage['zh-cn'] = {}
  if (!nextLanguage.en) nextLanguage.en = {}

  // 已有译文按中文原文索引，重复迁移时复用同一个 key
  const textToKey = new Map()
  Object.entries(nextLanguage['zh-cn']).forEach(([key, text]) => {
    if (typeof text === 'string' && text && !textToKey.has(text)) {
      textToKey.set(text, key)
    }
  })

  function uniqueKey(base) {
    let key = sanitizeKey(base)
    let n = 2
    while (
      Object.values(nextLanguage).some((dict) => Object.prototype.hasOwnProperty.call(dict, key))
    ) {
      key = `${sanitizeKey(base)}_${n}`
      n += 1
    }
    return key
  }

  function wrap(text, hint) {
    if (typeof text !== 'string' || !text || isLanguageToken(text)) return text
    if (!needsTranslation(text)) return text

    const existing = textToKey.get(text)
    if (existing) return `{{$t.${existing}}}`

    const key = uniqueKey(hint)
    textToKey.set(text, key)
    nextLanguage['zh-cn'][key] = text
    const english = legacyEnglish(text)
    if (english !== text) nextLanguage.en[key] = english
    return `{{$t.${key}}}`
  }

  function walk(list) {
    if (!Array.isArray(list)) return list
    return list.map((rule) => {
      if (!rule || typeof rule !== 'object') return rule
      const field = rule.field || rule.type || 'text'
      const next = { ...rule }
      if (typeof next.title === 'string') {
        next.title = wrap(next.title, `${field}_title`)
      }
      if (typeof next.info === 'string') {
        next.info = wrap(next.info, `${field}_info`)
      }
      if (next.props && typeof next.props.placeholder === 'string') {
        next.props = {
          ...next.props,
          placeholder: wrap(next.props.placeholder, `${field}_placeholder`),
        }
      }
      if (Array.isArray(next.options)) {
        next.options = next.options.map((option, index) => {
          if (!option || typeof option.label !== 'string') return option
          return { ...option, label: wrap(option.label, `${field}_option_${index}`) }
        })
      }
      if (Array.isArray(next.children)) {
        next.children = walk(next.children)
      }
      return next
    })
  }

  return { rules: walk(rules), language: nextLanguage }
}
