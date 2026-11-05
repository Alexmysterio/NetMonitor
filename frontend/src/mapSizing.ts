import type { CanvasElement } from './types'

const TITLE_FONT = '700 11.5px Inter, system-ui, -apple-system, sans-serif'
const SUB_FONT = '9.2px Inter, system-ui, -apple-system, sans-serif'
const MONO_FONT = '8.8px "JetBrains Mono", ui-monospace, SFMono-Regular, monospace'
const ZONE_FONT = '750 9px Inter, system-ui, -apple-system, sans-serif'
const MAX_CONTENT_WIDTH = 340
const MIN_CONTENT_WIDTH = 96

let measureContext: CanvasRenderingContext2D | null | undefined

function context(): CanvasRenderingContext2D | null {
  if (measureContext === undefined) {
    measureContext = typeof document === 'undefined' ? null : document.createElement('canvas').getContext('2d')
  }
  return measureContext
}

function textWidth(text: string, font: string): number {
  const ctx = context()
  if (!ctx) {
    const size = Number.parseFloat(font) || 12
    return text.length * size * 0.58
  }
  ctx.font = font
  return ctx.measureText(text).width
}

function wrappedLines(text: string, width: number, font: string): number {
  // 0.9 accounts for word wrapping and kerning differences between the canvas
  // measurement and the browser layout. Overestimating keeps the shape large
  // enough so text never spills outside the geometry.
  return Math.max(1, Math.ceil(textWidth(text, font) / Math.max(1, width * 0.9)))
}

function isMono(text: string): boolean {
  return /^\d+\.\d+\.\d+\.\d+/.test(text) || /^[0-9a-f:]{17}$/i.test(text)
}

function labelLines(element: CanvasElement): string[] {
  const raw = String(element.rendered_label || element.label_template || '').replace(/\r/g, '')
  const lines = raw.split('\n').map(line => line.trim()).filter(Boolean)
  if (lines.length > 0) return lines
  const fallback = element.submap?.name || element.device?.name
  if (fallback) return [fallback]
  if (element.element_type === 'text_label') return ['Texto']
  if (element.element_type === 'zone') return ['Zona']
  return ['Nodo']
}

function shapeFactor(shape: string): number {
  switch (shape) {
    case 'diamond':
      return 2.15
    case 'hexagon':
      return 1.7
    case 'circle':
      return 1.5
    case 'pill':
      return 1.2
    default:
      return 1
  }
}

export function elementDisplaySize(element: CanvasElement): { width: number; height: number } {
  const baseWidth = Math.max(0, Number(element.width) || 0)
  const baseHeight = Math.max(0, Number(element.height) || 0)
  const custom = (element.custom_style || {}) as Record<string, unknown>

  if (element.element_type === 'text_label') {
    const fontSize = Math.max(8, Number(custom.fontSize) || 12)
    const font = `${fontSize}px Inter, system-ui, -apple-system, sans-serif`
    const rawLines = String(element.rendered_label || element.label_template || 'Texto').replace(/\r/g, '').split('\n')
    const lines = rawLines.length > 0 ? rawLines : ['Texto']
    const widest = Math.max(...lines.map(line => textWidth(line, font)), 40)
    const contentWidth = Math.min(440, Math.max(60, widest))
    const lineCount = lines.reduce((total, line) => total + wrappedLines(line, contentWidth, font), 0)
    return {
      width: Math.max(baseWidth, Math.ceil(contentWidth + 20)),
      height: Math.max(baseHeight, Math.ceil(lineCount * fontSize * 1.45 + 14)),
    }
  }

  if (element.element_type === 'zone') {
    const title = labelLines(element)[0].toUpperCase()
    const width = Math.max(baseWidth, Math.min(MAX_CONTENT_WIDTH + 34, Math.ceil(textWidth(title, ZONE_FONT) + 34)))
    const lines = wrappedLines(title, Math.max(80, width - 34), ZONE_FONT)
    return {
      width,
      height: Math.max(baseHeight, Math.ceil(lines * 12 + 28)),
    }
  }

  const lines = labelLines(element)
  const title = lines[0]
  const sublines = lines.slice(1)
  const subFonts = sublines.map(line => (isMono(line) ? MONO_FONT : SUB_FONT))
  const widest = Math.max(
    40,
    textWidth(title, TITLE_FONT),
    ...sublines.map((line, i) => textWidth(line, subFonts[i])),
  )
  const contentWidth = Math.min(MAX_CONTENT_WIDTH, Math.max(MIN_CONTENT_WIDTH, widest))
  const titleLines = wrappedLines(title, contentWidth, TITLE_FONT)
  const subLineCount = sublines.reduce(
    (total, line, i) => total + wrappedLines(line, contentWidth, subFonts[i]),
    0,
  )
  const textHeight = titleLines * 14 + (subLineCount > 0 ? 4 + subLineCount * 12 : 0)
  const iconHeight = element.show_icon ? 48 : 0
  const clipped = element.shape === 'circle' || element.shape === 'diamond' || element.shape === 'hexagon'
  const footer = (element.element_type === 'submap' && !clipped ? 24 : 0) + (element.device?.root_cause ? 18 : 0)
  const contentHeight = iconHeight + textHeight + 26 + footer
  const factor = shapeFactor(element.shape)

  return {
    width: Math.max(baseWidth, 140, Math.ceil((contentWidth + 28) * factor)),
    height: Math.max(baseHeight, 78, Math.ceil(contentHeight * factor)),
  }
}
