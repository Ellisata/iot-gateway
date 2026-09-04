// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

/**
 * RSA 加密工具（前端版）
 *
 * 与后端 Go rsa.go 兼容：
 *   - 算法：RSA-OAEP / SHA-256
 *   - 密钥格式：Base64 编码的 PEM（与 Go 端生成/使用的格式一致）
 *   - 输出：Base64 编码的密文
 *
 * @see backend utils/rsa.go
 */

import forgeModule from 'node-forge'

// node-forge 以 CJS 导出，兼容不同打包器的 interop 形式
const forge = forgeModule.default || forgeModule

/** 后端提供的公钥（Base64 编码的 PEM） */
const PUBLIC_KEY_B64 = 'LS0tLS1CRUdJTiBSU0EgUFVCTElDIEtFWS0tLS0tCk1JSUJDZ0tDQVFFQXpWWnVvYkdZWWpmNVNtelRJY1Nnay9NT3d6emovQU94NkhHSk5OczNLYmlLVGFtZTMwaEUKb25BR0ZHT1A3MmllNzFGZ0tXREM3QzRRV3poOTNNNlE4V2Q2Z2pWUzJub05ueER6T3BKOVIwRnJWY3BlcDdoTQowVDFlajBxN0NtNWJaL3N4a0FERUVtVTVGcDQxOUczWUpTcm44NVVlUDdOQUpUVzJzUytNa2xmQlVqWVN1WGhkCjNhUVE0UWJndXZqb1U3WUtMa1VxMC9nV0l1STF6emV5WlRPQU4ya2xyNHVjS1FpQ2VUS0MvTlV0UmpRVmdGZ0MKZ0MwdHQ3ZlNUOHFUV0thR0NCTHhGYVJRRWNtaGZVRFpHT3hNSUVqcmc5d2N5aytTK2JIdE10V2VtS0N4MXo5LwozbzVSVitMQVdUTEpUTHRJTVlhNjU5cGpGYWRQMEJKdWlRSURBUUFCCi0tLS0tRU5EIFJTQSBQVUJMSUMgS0VZLS0tLS0K'

/**
 * 将 Base64 字符串转为 ArrayBuffer
 */
function base64ToArrayBuffer(b64) {
  const binaryStr = atob(b64)
  const bytes = new Uint8Array(binaryStr.length)
  for (let i = 0; i < binaryStr.length; i++) {
    bytes[i] = binaryStr.charCodeAt(i)
  }
  return bytes.buffer
}

/**
 * 将 ArrayBuffer 转为 Base64 字符串
 */
function arrayBufferToBase64(buffer) {
  const bytes = new Uint8Array(buffer)
  let binary = ''
  for (let i = 0; i < bytes.length; i++) {
    binary += String.fromCharCode(bytes[i])
  }
  return btoa(binary)
}

/* ========== DER 编码辅助函数 ========== */

/** 编码 ASN.1 长度（DER 长格式/短格式） */
function encodeDERLength(length) {
  if (length < 128) return new Uint8Array([length])
  const bytes = []
  let len = length
  while (len > 0) {
    bytes.unshift(len & 0xff)
    len >>= 8
  }
  return new Uint8Array([0x80 | bytes.length, ...bytes])
}

/** 构造 ASN.1 TLV（tag + length + value） */
function derEncode(tag, content) {
  const lengthBytes = encodeDERLength(content.length)
  const result = new Uint8Array(1 + lengthBytes.length + content.length)
  result[0] = tag
  result.set(lengthBytes, 1)
  result.set(content, 1 + lengthBytes.length)
  return result
}

/** 合并多个 Uint8Array */
function concatBytes(...arrays) {
  const total = arrays.reduce((sum, a) => sum + a.length, 0)
  const result = new Uint8Array(total)
  let offset = 0
  for (const arr of arrays) {
    result.set(arr, offset)
    offset += arr.length
  }
  return result
}

/**
 * 将 PKCS#1 格式的 RSA 公钥 DER 转换为 X.509 SPKI 格式
 *
 * SPKI = SEQUENCE {
 *   SEQUENCE { OID rsaEncryption, NULL },
 *   BIT STRING { <PKCS#1 DER> }
 * }
 */
function pkcs1ToSpki(pkcs1Der) {
  const pkcs1Bytes = new Uint8Array(pkcs1Der)

  // AlgorithmIdentifier: rsaEncryption OID (1.2.840.113549.1.1.1) + NULL
  const algId = new Uint8Array([
    0x30, 0x0d,
      0x06, 0x09, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x01,
      0x05, 0x00,
  ])

  // BIT STRING（前置 0x00 表示无未使用位）
  const bitString = derEncode(0x03, concatBytes(new Uint8Array([0x00]), pkcs1Bytes))

  // 外层 SEQUENCE
  return derEncode(0x30, concatBytes(algId, bitString)).buffer
}

/**
 * 将 Base64 编码的 PEM 公钥导入为 CryptoKey
 *
 * 步骤：
 *  1. 外层 Base64 解码 → 得到 PEM 文本
 *  2. 移除 PEM 头/尾标记和换行 → 得到裸 Base64
 *  3. Base64 解码 → DER 字节
 *  4. 检测密钥格式（PKCS#1 → 自动转为 SPKI）
 *  5. 以 SPKI 格式导入 Web Crypto API
 *
 * @param {string} b64Pem - Base64 编码的 PEM 公钥
 * @returns {Promise<CryptoKey>}
 */
async function importPublicKey(b64Pem) {
  // 1. 外层 Base64 解码 → PEM 文本
  const pemStr = atob(b64Pem)

  // 2. 检测密钥格式并提取 Base64 体
  const isPkcs1 = /-----BEGIN RSA PUBLIC KEY-----/.test(pemStr)
  const pemBody = pemStr
    .replace(/-----BEGIN (RSA )?PUBLIC KEY-----/g, '')
    .replace(/-----END (RSA )?PUBLIC KEY-----/g, '')
    .replace(/\r?\n/g, '')
    .trim()

  // 3. Base64 解码 → DER
  let derBuffer = base64ToArrayBuffer(pemBody)

  // 4. PKCS#1 → SPKI 转换（Web Crypto API 只接受 SPKI 格式）
  if (isPkcs1) {
    derBuffer = pkcs1ToSpki(derBuffer)
  }

  // 5. 导入为 CryptoKey
  return crypto.subtle.importKey(
    'spki',
    derBuffer,
    {
      name: 'RSA-OAEP',
      hash: { name: 'SHA-256' },
    },
    false,
    ['encrypt']
  )
}

/**
 * 判断 Web Crypto API（crypto.subtle）是否可用。
 *
 * subtle 仅在安全上下文（HTTPS 或 localhost）中暴露；生产环境以纯 HTTP + IP 访问时
 * （如 http://<IP>:9081/admin），crypto.subtle 为 undefined，需回退到纯 JS 实现。
 */
function isSubtleAvailable() {
  return typeof crypto !== 'undefined' && !!crypto.subtle
}

/**
 * 使用 node-forge 实现 RSA-OAEP / SHA-256 加密（纯 JS，不依赖安全上下文）。
 *
 * 供 crypto.subtle 不可用时兜底；OAEP 标签哈希与 MGF1 均用 SHA-256，
 * 与后端 rsaUtil.RSADecrypt（EncryptOAEP/DecryptOAEP(sha256.New())）完全兼容。
 *
 * @param {string} plaintext - 明文
 * @param {string} b64Pem - Base64 编码的 PEM 公钥（PKCS#1 或 SPKI 均可）
 * @returns {string} Base64 编码的密文
 */
function rsaEncryptWithForge(plaintext, b64Pem) {
  const pemStr = atob(b64Pem)
  const publicKey = forge.pki.publicKeyFromPem(pemStr)

  const encrypted = publicKey.encrypt(forge.util.encodeUtf8(plaintext), 'RSA-OAEP', {
    md: forge.md.sha256.create(),
  })

  return forge.util.encode64(encrypted)
}

/**
 * RSA 加密（OAEP / SHA-256）
 *
 * 优先使用 Web Crypto API（crypto.subtle）；在非安全上下文（纯 HTTP + IP）
 * 下自动回退到 node-forge 纯 JS 实现。
 *
 * @param {string} plaintext - 明文
 * @param {string} [publicKeyB64] - Base64 编码的 PEM 公钥，默认使用项目内置公钥
 * @returns {Promise<string>} Base64 编码的密文
 */
export async function rsaEncrypt(plaintext, publicKeyB64 = PUBLIC_KEY_B64) {
  if (!isSubtleAvailable()) {
    return rsaEncryptWithForge(plaintext, publicKeyB64)
  }

  const publicKey = await importPublicKey(publicKeyB64)

  const encoded = new TextEncoder().encode(plaintext)
  const ciphertext = await crypto.subtle.encrypt(
    {
      name: 'RSA-OAEP',
    },
    publicKey,
    encoded
  )

  return arrayBufferToBase64(ciphertext)
}

export default { rsaEncrypt, PUBLIC_KEY_B64 }
