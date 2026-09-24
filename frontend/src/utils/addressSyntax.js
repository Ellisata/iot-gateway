// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

/**
 * 各协议的地址写法：模板、示例、常见错误。
 *
 * 为什么需要：device_address.name 存的是**协议地址本体**（D100 / 40001 / DB1.DBD10 /
 * 02010100），而地址语法随协议完全不同 —— 同一个 "D100" 在三菱指 D 寄存器、
 * 在欧姆龙 FINS 指 DM 区。后端在新增/修改时不解析地址、只查重名，填错了不报错，
 * 直到驱动读取失败把该点位标成 Quality=0，才表现为"数据没上来"。
 * 所以这里是用户填地址时唯一的信息来源。
 *
 * 为什么留在前端（而不是像协议列表那样由后端下发）：协议**列表**是后端枚举，
 * 但地址语法是文案。本项目的既定分工见 utils/alarmWebhookType.js —— 后端返回的
 * 文案是中文，直接用会让英文界面混进中文，所以展示文案一律由前端按语言取。
 *
 * 取证来源（改驱动时请同步这里）：
 *   backend/driver/<协议>/address.go|parser.go 解析函数上的注释
 *   backend/driver/<协议>/ 下各 address_test.go、parser_test.go 的用例
 *     （示例优先取这里，保证真实可用）
 *
 * 注意：本注释块里不要出现星号紧跟斜杠的通配路径，那会提前结束块注释。
 *
 * 本文件只放**语言中性**的东西：协议名映射、字面地址示例、i18n key。
 * 任何一句要给用户看的话都必须走 i18n —— 语法模板也一样，
 * 它看着像符号，其实含「或」「字元件」「原样发送」这类中文词。
 *
 * 与 premium 版的差异：premium 多一个 ABB.RWS 插件（此处无），
 * 其余 14 个协议名与两份 addressSyntax.js 应当保持一致。
 */

/** 协议名（iot_protocol.name）→ 规格名。多个协议名共用一份规格：同一驱动、同一套语法。 */
const PROTOCOL_SPEC = {
  'ModBus.TCP': 'modbus',
  'ModBus.RTU': 'modbus',
  'Siemens.S7': 's7',
  'Mitsubishi.MC.TCP': 'mc',
  'Mitsubishi.MC.Serial': 'mc',
  'Omron.FINS.UDP': 'fins',
  'Omron.FINS.TCP': 'fins',
  'Omron.FINS.Serial': 'fins',
  'Omron.FINS.HostLinkTCP': 'fins',
  'Omron.CIP': 'omronCip',
  'Rockwell.CIP': 'rockwellCip',
  'OPC.UA': 'opcua',
  'DLT645.Serial': 'dlt645',
  'DLT645.TCP': 'dlt645',
}

/**
 * templateKey : 语法模板的 i18n key（模板里含中文虚词，不能原样展示）。
 * examples    : 字面地址示例，语言中性；**第一个同时用作输入框占位符**。
 * pitfallKeys : 常见错误的 i18n key。
 */
const ADDRESS_SPECS = {
  modbus: {
    templateKey: 'deviceAddress.syntaxModbus',
    examples: ['40001', '00001', '%MW100'],
    pitfallKeys: ['deviceAddress.pitfallModbusBase', 'deviceAddress.pitfallModbusShort'],
  },
  s7: {
    templateKey: 'deviceAddress.syntaxS7',
    examples: ['DB1.DBW4', 'DB1.DBD10', 'M0.1'],
    pitfallKeys: ['deviceAddress.pitfallS7Width'],
  },
  mc: {
    templateKey: 'deviceAddress.syntaxMc',
    examples: ['D100', 'M0', 'X30'],
    pitfallKeys: ['deviceAddress.pitfallMcOctal', 'deviceAddress.pitfallMcBitDevice'],
  },
  fins: {
    templateKey: 'deviceAddress.syntaxFins',
    examples: ['D100', 'CIO0.00', 'W1.2'],
    pitfallKeys: ['deviceAddress.pitfallFinsBare'],
  },
  omronCip: {
    templateKey: 'deviceAddress.syntaxOmronCip',
    examples: ['Tag_0', 'MotorSpeed', 'Recipe.Setpoint'],
    pitfallKeys: ['deviceAddress.pitfallCipNoArea'],
  },
  rockwellCip: {
    templateKey: 'deviceAddress.syntaxRockwellCip',
    examples: ['MotorSpeed', 'MotorSpeed[0]', 'Program:MainProgram.MyTag'],
    pitfallKeys: ['deviceAddress.pitfallRockwellArray', 'deviceAddress.pitfallRockwellLazy'],
  },
  opcua: {
    templateKey: 'deviceAddress.syntaxOpcua',
    examples: ['ns=1;i=1001', 'ns=2;s=Temperature', 'Root/Objects/2:Device/2:Temp'],
    pitfallKeys: ['deviceAddress.pitfallOpcuaSlash', 'deviceAddress.pitfallOpcuaRoot'],
  },
  dlt645: {
    templateKey: 'deviceAddress.syntaxDlt645',
    examples: ['02010100', '00000000', '12345678:2:0:s'],
    pitfallKeys: [
      'deviceAddress.pitfallDltDict',
      'deviceAddress.pitfallDltVersion',
      'deviceAddress.pitfallDltType',
    ],
  },
}

/**
 * 取某协议的地址规格。协议未登记或为空时返回 null，由调用方回落到通用文案 ——
 * 宁可不展示帮助，也不给一份可能不对的说明。
 */
export function getAddressSpec(protocolName) {
  if (!protocolName) return null
  const family = PROTOCOL_SPEC[protocolName]
  return family ? ADDRESS_SPECS[family] || null : null
}

/** 取地址示例（规格里的第一个例子），同时用作输入框占位符与常驻提示。 */
export function getAddressExample(protocolName) {
  return getAddressSpec(protocolName)?.examples?.[0] || ''
}
