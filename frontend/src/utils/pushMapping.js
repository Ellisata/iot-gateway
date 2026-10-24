// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// 出站映射（mapping-json）的可视化模型与纯函数。
//
// 这里的 state 只服务于编辑器；真正保存的是 stateToMapping() 的结果。
// 契约真源：backend/push/codec/mappingjson 与 backend/mappingdsl。

export const RECORD_DOC_KEYS = [
  'deviceId',
  'deviceName',
  'deviceAddressId',
  'deviceAddressName',
  'deviceAddressLabel',
  'value',
  'dataType',
  'kind',
  'protocol',
  'quality',
  'collectedAt',
]

export const BATCH_DOC_KEYS = ['deviceId', 'collectedAt', 'count']

export const SOURCES = ['path', 'const', 'template', 'map']
export const FIELD_TYPES = ['auto', 'string', 'number', 'boolean']
export const SHAPES = ['array', 'object', 'record']

export const TRANSFORM_OPS = [
  'scale',
  'round',
  'trim',
  'default',
  'hexDecode',
  'base64Decode',
  'bitfield',
  'regex',
]

// 含花括号的示例只能放在这里：vue-i18n 会把 {...} 当成插值。
export const TEMPLATE_PLACEHOLDER = 'gateway/{deviceId}'
export const KEY_PLACEHOLDER = 'deviceId 或 device.id'

export class MappingValidationError extends Error {
  constructor(problem) {
    super('mapping validation failed')
    this.name = 'MappingValidationError'
    this.problem = problem
  }
}

let seq = 0
function nextId() {
  seq += 1
  return `map-row-${seq}`
}

// ---------- 行列模型 ----------

export function createRow(overrides = {}) {
  return {
    id: nextId(),
    key: '',
    source: 'path',
    value: '',
    type: 'auto',
    default: '',
    transform: [],
    mappingRows: [],
    expanded: false,
    ...overrides,
  }
}

export function createTransformRow(op = 'scale') {
  const row = {
    id: nextId(),
    op,
    factor: '',
    offset: '',
    digits: '',
    value: '',
    pattern: '',
    group: '',
    length: '',
  }
  applyTransformDefaults(row)
  return row
}

export function applyTransformDefaults(row) {
  switch (row.op) {
    case 'round':
      if (row.digits === '') row.digits = '2'
      break
    case 'bitfield':
      if (row.offset === '') row.offset = '0'
      if (row.length === '') row.length = '8'
      break
    case 'regex':
      if (row.group === '') row.group = '0'
      break
    default:
      break
  }
  return row
}

export function createEmptyState() {
  return {
    version: 1,
    shape: 'array',
    envelope: '',
    fields: [],
    batchFields: [],
  }
}

export function defaultMappingState() {
  const state = createEmptyState()
  fillCommonFields(state)
  return state
}

export function isEmptyMappingState(state) {
  const hasRows = (rows) => (rows || []).some((r) => String(r.key || '').trim() !== '')
  return (
    !hasRows(state?.fields) &&
    !hasRows(state?.batchFields) &&
    !String(state?.envelope || '').trim()
  )
}

const COMMON_ROWS = [
  { key: 'deviceId', value: 'deviceId' },
  { key: 'deviceAddressId', value: 'deviceAddressId' },
  { key: 'value', value: 'value', type: 'number' },
  { key: 'quality', value: 'quality', type: 'number' },
  { key: 'collectedAt', value: 'collectedAt' },
]

/** 追加常用字段；已存在的输出键不覆盖。 */
export function fillCommonFields(state) {
  if (!state || !Array.isArray(state.fields)) return state
  const existing = new Set(
    state.fields.map((r) => String(r.key || '').trim()).filter(Boolean),
  )
  COMMON_ROWS.forEach((def) => {
    if (existing.has(def.key)) return
    state.fields.push(
      createRow({
        key: def.key,
        source: 'path',
        value: def.value,
        type: def.type || 'auto',
      }),
    )
  })
  return state
}

export function applyPreset(state, preset) {
  if (!state) return state
  if (preset === 'flat') {
    state.shape = 'array'
  } else if (preset === 'record') {
    state.shape = 'record'
  } else if (preset === 'envelope') {
    state.shape = 'object'
    if (!String(state.envelope || '').trim()) state.envelope = 'data'
  }
  return state
}

export function insertTemplateToken(text, caret, path) {
  const source = String(text || '')
  const pos = Number.isInteger(caret) && caret >= 0 ? Math.min(caret, source.length) : source.length
  const token = `{${path}}`
  return {
    text: source.slice(0, pos) + token + source.slice(pos),
    caret: pos + token.length,
  }
}

// ---------- state -> mapping ----------

function putRow(target, row) {
  const key = String(row.key || '').trim()
  if (!key) return
  const out = { source: row.source || 'path', value: String(row.value ?? '') }
  if (row.source === 'map') {
    const mapping = {}
    for (const pair of row.mappingRows || []) {
      const from = String(pair.from ?? '')
      const to = String(pair.to ?? '')
      if (from !== '' && to !== '') mapping[from] = to
    }
    if (Object.keys(mapping).length) out.mapping = mapping
  }
  const def = String(row.default ?? '')
  if (def !== '') out.default = def
  if (row.type && row.type !== 'auto') out.type = row.type
  const transform = (row.transform || [])
    .map(transformToJSON)
    .filter(Boolean)
  if (transform.length) out.transform = transform
  target[key] = out
}

function toNumber(value) {
  if (value === '' || value == null) return undefined
  const n = Number(value)
  return Number.isFinite(n) ? n : undefined
}

function transformToJSON(t) {
  if (!t || !TRANSFORM_OPS.includes(t.op)) return null
  const out = { op: t.op }
  switch (t.op) {
    case 'scale': {
      const factor = toNumber(t.factor)
      const offset = toNumber(t.offset)
      if (factor !== undefined) out.factor = factor
      if (offset !== undefined) out.offset = offset
      break
    }
    case 'round': {
      const digits = toNumber(t.digits)
      if (digits !== undefined) out.digits = digits
      break
    }
    case 'default':
      out.value = String(t.value ?? '')
      break
    case 'bitfield':
      out.offset = toNumber(t.offset) ?? 0
      out.length = toNumber(t.length) ?? 0
      break
    case 'regex':
      out.pattern = String(t.pattern ?? '')
      {
        const group = toNumber(t.group)
        if (group !== undefined) out.group = group
      }
      break
    default:
      break
  }
  return out
}

export function stateToMapping(state) {
  const out = {
    version: Number(state?.version) === 0 ? 0 : 1,
    fields: {},
  }
  for (const row of state?.fields || []) putRow(out.fields, row)

  if (state?.shape === 'record') {
    out.emit = 'record'
    return out
  }
  if (state?.shape === 'object') {
    out.emit = 'batch'
    const envelope = String(state.envelope || '').trim()
    if (envelope) out.envelope = envelope
    const batchFields = {}
    for (const row of state.batchFields || []) putRow(batchFields, row)
    if (Object.keys(batchFields).length) out.batchFields = batchFields
  }
  return out
}

// ---------- mapping -> state ----------

const TOP_KEYS = new Set(['version', 'emit', 'envelope', 'batchFields', 'fields'])
const FIELD_KEYS = new Set(['source', 'value', 'mapping', 'default', 'transform', 'type'])
const TRANSFORM_KEYS = new Set([
  'op',
  'factor',
  'offset',
  'digits',
  'value',
  'pattern',
  'group',
  'length',
])

function fail(code, params = {}) {
  return { ok: false, code, params }
}

function isPlainObject(value) {
  return !!value && typeof value === 'object' && !Array.isArray(value)
}

/**
 * 把后端配置转换成编辑器 state。
 * 不可表示时返回 { ok:false, code, params }，调用方应保留 JSON 文本而不是丢弃字段。
 */
export function mappingToState(mapping) {
  if (mapping == null) return { ok: true, state: createEmptyState() }
  if (!isPlainObject(mapping)) return fail('notObject')

  for (const key of Object.keys(mapping)) {
    if (!TOP_KEYS.has(key)) return fail('unknownTopKey', { key })
  }

  const version = mapping.version == null ? 1 : mapping.version
  if (version !== 0 && version !== 1) return fail('unsupportedVersion')

  const emit =
    mapping.emit == null ? '' : String(mapping.emit).trim().toLowerCase()
  if (emit !== '' && emit !== 'batch' && emit !== 'record') {
    return fail('invalidEmit')
  }
  if (mapping.envelope != null && typeof mapping.envelope !== 'string') {
    return fail('badSectionShape', { section: 'envelope' })
  }
  if (mapping.fields == null || !isPlainObject(mapping.fields)) {
    return fail('badSectionShape', { section: 'fields' })
  }
  let batchFields = {}
  if (mapping.batchFields != null) {
    if (!isPlainObject(mapping.batchFields)) {
      return fail('badSectionShape', { section: 'batchFields' })
    }
    batchFields = mapping.batchFields
  }

  const fields = []
  for (const [key, value] of Object.entries(mapping.fields)) {
    const parsed = fieldToRow(key, value)
    if (!parsed.ok) return parsed
    fields.push(parsed.row)
  }
  const batchRows = []
  for (const [key, value] of Object.entries(batchFields)) {
    const parsed = fieldToRow(key, value)
    if (!parsed.ok) return parsed
    batchRows.push(parsed.row)
  }

  let shape = 'array'
  if (emit === 'record') shape = 'record'
  else if (String(mapping.envelope || '').trim() || batchRows.length) shape = 'object'

  return {
    ok: true,
    state: {
      version,
      shape,
      envelope: String(mapping.envelope || ''),
      fields,
      batchFields: batchRows,
    },
  }
}

function fieldToRow(key, value, section = 'fields') {
  if (!isPlainObject(value)) return fail('badFieldShape', { key, section })
  for (const prop of Object.keys(value)) {
    if (!FIELD_KEYS.has(prop)) return fail('unknownFieldKey', { key, prop })
  }
  const source = String(value.source || 'path').trim().toLowerCase()
  if (!SOURCES.includes(source)) {
    if (source === 'topic') return fail('sourceTopicUnsupported', { key })
    return fail('unknownFieldKey', { key, prop: 'source' })
  }
  if (value.value != null && typeof value.value !== 'string') {
    return fail('badFieldValue', { key })
  }
  const type = String(value.type || 'auto').trim().toLowerCase()
  if (!FIELD_TYPES.includes(type)) return fail('invalidType', { key })

  let mappingRows = []
  if (value.mapping != null) {
    if (!isPlainObject(value.mapping)) return fail('badMappingPairs', { key })
    for (const [from, to] of Object.entries(value.mapping)) {
      if (typeof to !== 'string') return fail('badMappingPairs', { key })
      mappingRows.push({ from, to })
    }
  }

  const transform = []
  if (value.transform != null) {
    if (!Array.isArray(value.transform)) return fail('badTransform', { key })
    for (const t of value.transform) {
      const parsed = transformToRow(t, key)
      if (!parsed.ok) return parsed
      transform.push(parsed.row)
    }
  }

  return {
    ok: true,
    row: createRow({
      key,
      source,
      value: String(value.value ?? ''),
      type,
      default: value.default == null ? '' : String(value.default),
      mappingRows,
      transform,
    }),
  }
}

function transformToRow(t, key) {
  if (!isPlainObject(t)) return fail('badTransform', { key })
  for (const prop of Object.keys(t)) {
    if (!TRANSFORM_KEYS.has(prop)) return fail('badTransform', { key })
  }
  const op = String(t.op || '')
  if (!TRANSFORM_OPS.includes(op)) return fail('badTransform', { key })
  const numeric = ['factor', 'offset', 'digits', 'group', 'length']
  for (const prop of numeric) {
    if (t[prop] != null && typeof t[prop] !== 'number') {
      return fail('badTransform', { key })
    }
  }
  for (const prop of ['value', 'pattern']) {
    if (t[prop] != null && typeof t[prop] !== 'string') {
      return fail('badTransform', { key })
    }
  }
  return {
    ok: true,
    row: {
      id: nextId(),
      op,
      factor: t.factor == null ? '' : String(t.factor),
      offset: t.offset == null ? '' : String(t.offset),
      digits: t.digits == null ? '' : String(t.digits),
      value: t.value == null ? '' : t.value,
      pattern: t.pattern == null ? '' : t.pattern,
      group: t.group == null ? '' : String(t.group),
      length: t.length == null ? '' : String(t.length),
    },
  }
}

// ---------- 校验 ----------

function error(code, extra = {}) {
  return { level: 'error', code, ...extra }
}

function warning(code, extra = {}) {
  return { level: 'warning', code, ...extra }
}

function segmentsOf(key) {
  return key.split('.')
}

/** 结构校验；后端仍是最终权威。 */
export function validateState(state) {
  const problems = []
  const shape = state?.shape || 'array'
  const version = Number(state?.version)
  if (version !== 0 && version !== 1) problems.push(error('unsupportedVersion'))
  if (!SHAPES.includes(shape)) problems.push(error('invalidShape'))

  const fieldRows = state?.fields || []
  if (!fieldRows.some((r) => String(r.key || '').trim())) {
    problems.push(error('noFields'))
  }
  validateRows(fieldRows, 'fields', RECORD_DOC_KEYS, problems)
  if (shape === 'object') {
    validateRows(state?.batchFields || [], 'batchFields', BATCH_DOC_KEYS, problems)
  }

  const envelope = String(state?.envelope || '').trim()
  if (shape === 'object') {
    if (envelope && /[\s.]/.test(envelope)) {
      problems.push(error('envelopeInvalidChars'))
    }
    const hasBatch = (state?.batchFields || []).some((r) => String(r.key || '').trim())
    const effective = envelope || (hasBatch ? 'records' : '')
    if (!effective) {
      problems.push(error('envelopeMissing'))
    } else {
      for (const row of state?.batchFields || []) {
        const key = String(row.key || '').trim()
        if (!key) continue
        if (segmentsOf(key)[0] === effective) {
          problems.push(error('envelopeCollision', { key: row.key, envelope: effective }))
        }
      }
    }
  } else if (shape === 'record') {
    const hasExtras =
      !!envelope || (state?.batchFields || []).some((r) => String(r.key || '').trim())
    if (hasExtras) problems.push(warning('recordIgnoredExtras'))
  }

  return problems
}

function validateRows(rows, section, allowedPaths, problems) {
  const seen = new Map()
  const validKeys = []
  const allowed = new Set(allowedPaths)

  for (const row of rows) {
    const key = String(row.key || '').trim()
    if (!key) {
      problems.push(error('emptyKey', { section, rowId: row.id }))
      continue
    }
    if (key.startsWith('$')) {
      problems.push(error('dollarKey', { section, rowId: row.id, key }))
    }
    if (segmentsOf(key).some((seg) => seg.trim() === '')) {
      problems.push(error('emptySegment', { section, rowId: row.id, key }))
    }
    if (seen.has(key)) {
      problems.push(error('duplicateKey', { section, rowId: row.id, key }))
    }
    seen.set(key, row)
    validKeys.push({ key, row })

    if (!FIELD_TYPES.includes(row.type || 'auto')) {
      problems.push(error('invalidType', { section, rowId: row.id, key }))
    }

    const source = SOURCES.includes(row.source) ? row.source : 'path'
    if (source !== 'const' && String(row.value ?? '').trim() === '') {
      problems.push(error('missingValue', { section, rowId: row.id, key }))
    }
    if (
      (source === 'path' || source === 'map') &&
      String(row.value ?? '').trim() &&
      !allowed.has(String(row.value).trim())
    ) {
      problems.push(warning('unknownPath', { section, rowId: row.id, key }))
    }
    if (source === 'map') {
      const pairs = (row.mappingRows || []).filter(
        (p) => String(p.from ?? '') !== '' || String(p.to ?? '') !== '',
      )
      if (pairs.length === 0) {
        problems.push(error('mapNoPairs', { section, rowId: row.id, key }))
      }
      const seenFrom = new Set()
      for (const pair of pairs) {
        const from = String(pair.from ?? '')
        const to = String(pair.to ?? '')
        if (from === '' || to === '') {
          problems.push(error('mapIncompletePair', { section, rowId: row.id, key }))
          continue
        }
        const norm = from.trim().toLowerCase()
        if (seenFrom.has(norm)) {
          problems.push(error('mapDuplicateFrom', { section, rowId: row.id, key, from }))
        }
        seenFrom.add(norm)
      }
    }
    validateTransforms(row, section, problems)
  }

  for (let i = 0; i < validKeys.length; i += 1) {
    for (let j = 0; j < validKeys.length; j += 1) {
      if (i === j) continue
      const a = validKeys[i].key
      const b = validKeys[j].key
      if (b.startsWith(`${a}.`)) {
        problems.push(
          error('prefixConflict', { section, a, b, rowId: validKeys[j].row.id }),
        )
      }
    }
  }
}

function validateTransforms(row, section, problems) {
  for (const t of row.transform || []) {
    const base = { section, rowId: row.id, key: row.key }
    if (!TRANSFORM_OPS.includes(t.op)) {
      problems.push(error('badTransform', base))
      continue
    }
    if (t.op === 'default' && String(t.value ?? '').trim() === '') {
      problems.push(error('transformDefaultValue', base))
    }
    if (t.op === 'scale') {
      for (const param of ['factor', 'offset']) {
        if (t[param] !== '' && !Number.isFinite(Number(t[param]))) {
          problems.push(error('transformNumberInvalid', { ...base, param }))
        }
      }
    }
    if (t.op === 'round') {
      const digits = t.digits === '' ? 0 : Number(t.digits)
      if (!Number.isInteger(digits) || digits < 0 || digits > 12) {
        problems.push(error('transformDigitsRange', base))
      }
    }
    if (t.op === 'bitfield') {
      const offset = t.offset === '' ? 0 : Number(t.offset)
      const length = Number(t.length)
      if (!Number.isInteger(offset) || offset < 0 || offset > 63) {
        problems.push(error('transformBitfieldOffset', base))
      }
      if (!Number.isInteger(length) || length < 1 || offset + length > 64) {
        problems.push(error('transformBitfieldLength', base))
      }
    }
    if (t.op === 'regex') {
      if (String(t.pattern ?? '') === '') {
        problems.push(error('transformRegexPattern', base))
      }
      const group = t.group === '' ? 0 : Number(t.group)
      if (!Number.isInteger(group) || group < 0) {
        problems.push(error('transformGroup', base))
      }
    }
  }
}

export function firstError(problems) {
  return (problems || []).find((p) => p.level === 'error') || null
}
