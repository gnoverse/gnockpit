// Click to copy: a value goes to the clipboard, and a toast says whether
// it went.

import { $in } from './dom.js'

/** @typedef {{ x: number, y: number }} ToastPlace where a toast shows, in px from the viewport's top left */

let _copyToastTimer = 0
// copyNotify copies text and says in a toast whether it could. A
// refused copy, or a browser without a clipboard (a page served
// over plain HTTP), is logged; it never rejects.
/**
 * @param {string | null} text
 * @param {MouseEvent} e
 * @returns {Promise<void>}
 */
export async function copyNotify(text, e) {
    if (!text || text === '-' || text === '—') return
    const at = copyToastPlace(e)
    if (!navigator.clipboard) {
        console.warn(
            'copy failed: no Clipboard API, which a browser offers only to a page served over HTTPS or from this machine'
        )
        showCopyToast('Copy failed', true, at)
        return
    }
    try {
        await navigator.clipboard.writeText(text)
    } catch (err) {
        console.warn('copy failed:', err)
        showCopyToast('Copy failed', true, at)
        return
    }
    showCopyToast('Copied!', false, at)
}
// copyToastPlace is where the toast of the copy click e asked for
// goes: 28 px above the pointer, or over the top of the control a
// key pressed, as such a click has no pointer position. A copy
// click is on an element: no other target has a box.
/**
 * @param {MouseEvent} e
 * @returns {ToastPlace}
 */
function copyToastPlace(e) {
    if (e.detail > 0 || !(e.target instanceof Element))
        return { x: e.clientX, y: e.clientY - 28 }
    const r = e.target.getBoundingClientRect()
    return { x: r.left + r.width / 2, y: r.top }
}
// showCopyToast shows text in the page's toast at at, for 800 ms.
/**
 * @param {string} text
 * @param {boolean} failed
 * @param {ToastPlace} at
 */
function showCopyToast(text, failed, at) {
    const toast = $in(document, '.copy-toast')
    toast.textContent = text
    toast.classList.toggle('failed', failed)
    toast.style.left = at.x + 'px'
    toast.style.top = at.y + 'px'
    toast.style.opacity = '1'
    clearTimeout(_copyToastTimer)
    _copyToastTimer = setTimeout(function () {
        toast.style.opacity = '0'
    }, 800)
}

export function start() {
    // KV values: the Network State and Block ETA cards, a validator's
    // profile
    document.addEventListener('click', function (e) {
        if (!(e.target instanceof Element)) return
        const v = e.target.closest('.kv .v')
        if (!v) return
        // Don't interfere with links or buttons inside the value
        if (e.target.closest('a, button')) return
        const text = v.getAttribute('data-copy') || v.textContent.trim()
        copyNotify(text, e)
    })

    // Table cells with data-copy attribute
    document.addEventListener('click', function (e) {
        if (!(e.target instanceof Element)) return
        const td = e.target.closest('.node-list-card td[data-copy]')
        if (!td) return
        if (e.target.closest('a, button, .info-btn')) return
        copyNotify(td.getAttribute('data-copy'), e)
    })

    // Block boxes — a click copies the block as text
    document.addEventListener('click', function (e) {
        if (!(e.target instanceof Element)) return
        const block = e.target.closest('#recent-blocks-body .info-btn')
        if (!block) return
        const tip = block.getAttribute('data-copy')
        if (tip) copyNotify(tip, e)
    })
}
