export async function copyToClipboard(text: string): Promise<boolean> {
  if (typeof window !== 'undefined' && window.isSecureContext && navigator?.clipboard?.writeText) {
    const ok = await navigator.clipboard.writeText(text).then(() => true).catch(() => false)
    if (ok) return true
  }

  if (typeof document === 'undefined') {
    return false
  }

  try {
    const textarea = document.createElement('textarea')
    textarea.value = text
    textarea.style.position = 'fixed'
    textarea.style.top = '0'
    textarea.style.left = '0'
    textarea.style.opacity = '0'
    textarea.style.pointerEvents = 'none'

    document.body.appendChild(textarea)
    textarea.focus()
    textarea.select()

    let successful = false
    try {
      successful = document.execCommand('copy')
    } finally {
      document.body.removeChild(textarea)
    }
    return Boolean(successful)
  } catch {
    return false
  }
}
