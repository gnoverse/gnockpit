// The Recent Blocks card: a box per block, coloured by its signatures
// and its transactions, and the card's compact view.

/** @import { BlockInfo } from '../types/node.js' */
/** @import { PopRow } from './popover.js' */

import { compareNames } from './collate.js'
import { $, clearChildren, mkEl } from './dom.js'
import { focusPlace, focusPlaceIn } from './focus.js'
import { b64toHex, twoDigits } from './format.js'
import { popRows } from './popover.js'
import { present } from './present.js'
import { live } from './state.js'
import { storageGet, storageSet } from './storage.js'

let lastRecentBlockHeight = 0

// renderRecentBlocks draws a box per block of blocks, which come
// oldest first: the newest block's box first, flashing when the block
// is new. Without blocks the card stays as it is.
/** @param {BlockInfo[] | null} blocks */
export function renderRecentBlocks(blocks) {
    const container = $('recent-blocks')
    const body = $('recent-blocks-body')
    if (!blocks || blocks.length === 0) return
    container.style.display = ''
    const focused = focusPlaceIn(body)
    clearChildren(body)
    if (rbCompact) {
        const rbGap = window.innerWidth < 600 ? '1px' : '2px'
        body.style.cssText =
            'display:flex;gap:' +
            rbGap +
            ';padding-bottom:4px;font-size:11px;overflow:hidden'
    } else {
        body.style.cssText =
            'display:flex;gap:2px;overflow-x:auto;white-space:nowrap;padding-bottom:4px;font-size:11px'
    }
    const newestHeight = Number(present(blocks[blocks.length - 1]).height)
    for (let i = blocks.length - 1; i >= 0; i--) {
        const b = present(blocks[i])
        const allSigned = b.signers >= b.total
        const hasTxs = b.num_txs > 0
        // Hue encodes signing (green = all signed, yellow = missing);
        // intensity encodes activity. Blocks with transactions get a
        // saturated fill and full-strength border, empty blocks recede,
        // so block activity is distinguishable at a glance.
        const rgb = allSigned ? '63,185,80' : '210,153,34'
        const bg = 'rgba(' + rgb + ',' + (hasTxs ? '0.45' : '0.13') + ')'
        const border = 'rgba(' + rgb + ',' + (hasTxs ? '1' : '0.4') + ')'
        // The newest block is the blocks' one tab stop.
        const box = mkEl('div', null, 'info-btn', {
            tabindex: i === blocks.length - 1 ? '0' : '-1',
            'data-key': b.height,
        })
        if (rbCompact) {
            // A compact box shows no text: its name says which
            // block it is.
            box.setAttribute('aria-label', 'block #' + b.height)
            box.style.cssText =
                'border-radius:2px;border:1px solid ' +
                border +
                ';background:' +
                bg +
                ';height:28px;cursor:default;flex:1;min-width:3px'
        } else {
            box.style.cssText =
                'display:inline-flex;flex-direction:column;align-items:center;padding:2px 4px;border-radius:3px;border:1px solid ' +
                border +
                ';background:' +
                bg +
                ';min-width:32px;font-size:8px;cursor:default;flex-shrink:0'
            const hSpan = mkEl('div', b.height, null)
            hSpan.style.color = 'var(--fg2)'
            const sSpan = mkEl('div', b.signers + '/' + b.total, null)
            sSpan.style.fontWeight = 'bold'
            sSpan.style.fontSize = '10px'
            box.appendChild(hSpan)
            box.appendChild(sSpan)
        }
        if (
            i === blocks.length - 1 &&
            Number.isFinite(newestHeight) &&
            newestHeight > lastRecentBlockHeight
        ) {
            box.style.background = 'rgba(88,166,255,0.5)'
            box.style.borderColor = '#58a6ff'
            box.style.transition =
                'background 2s ease-out, border-color 2s ease-out'
            ;(function (el, fb, fc) {
                requestAnimationFrame(function () {
                    requestAnimationFrame(function () {
                        el.style.background = fb
                        el.style.borderColor = fc
                    })
                })
            })(box, bg, border)
        }
        // The popover shows the block as rows; a click copies the
        // same as text, with the full app hash.
        let tip =
            '#' + b.height + ' \u2014 ' + b.signers + '/' + b.total + ' signed'
        /** @type {PopRow[]} */
        const rows = [[null, 'signed', b.signers + '/' + b.total]]
        if (b.time) {
            const dt = new Date(b.time)
            const when =
                dt.getUTCFullYear() +
                '-' +
                twoDigits(dt.getUTCMonth() + 1) +
                '-' +
                twoDigits(dt.getUTCDate()) +
                ' ' +
                twoDigits(dt.getUTCHours()) +
                ':' +
                twoDigits(dt.getUTCMinutes()) +
                ':' +
                twoDigits(dt.getUTCSeconds()) +
                ' UTC'
            tip += '\nTime: ' + when
            rows.push([null, 'time', when])
        }
        if (b.block_ms !== 0) {
            tip += '\nBlock time: ' + b.block_ms + 'ms'
            rows.push([null, 'block time', b.block_ms + ' ms'])
        }
        if (typeof b.num_txs === 'number') {
            tip += '\nTxs: ' + b.num_txs
            rows.push([null, 'txs', String(b.num_txs)])
        }
        let fullAppHash = ''
        if (b.app_hash) {
            fullAppHash = b64toHex(b.app_hash)
            const shortHash =
                fullAppHash.substring(0, 8) +
                '\u2026' +
                fullAppHash.substring(fullAppHash.length - 8)
            tip += '\nAppHash: ' + shortHash
            rows.push([null, 'app hash', shortHash])
        }
        if (b.missing && b.missing.length > 0) {
            const sorted = b.missing.slice().sort(function (a, b) {
                return compareNames(a.name || a.address, b.name || b.address)
            })
            tip += '\nMissing:'
            for (const [j, m] of sorted.entries()) {
                tip +=
                    '\n  ' +
                    (m.name ? m.name + ' (' + m.address + ')' : m.address)
                rows.push([null, j ? '' : 'missing', m.name || m.address])
            }
        }
        popRows(box, '#' + b.height, rows)
        box.setAttribute(
            'data-copy',
            fullAppHash
                ? tip.replace(/\nAppHash: .*/, '\nAppHash: ' + fullAppHash)
                : tip
        )
        body.appendChild(box)
    }
    if (Number.isFinite(newestHeight)) lastRecentBlockHeight = newestHeight
    focusPlace(focused)
    // The focused block left the window: the focus goes to the
    // nearest block left rather than to the page.
    if (focused && !body.contains(document.activeElement)) {
        const lost = Number(focused.key)
        /** @type {{ box: HTMLElement, off: number } | null} */
        let nearest = null
        for (const box of blockBoxes(body)) {
            const off = Math.abs(Number(box.dataset.key) - lost)
            if (!nearest || off < nearest.off) nearest = { box: box, off: off }
        }
        if (nearest) nearest.box.focus({ preventScroll: true })
    }
}

// blockBoxes are the boxes in body, the card's body: its children
// are the boxes renderRecentBlocks builds, mkEl's divs, and nothing
// else, so each is an HTML element.
/**
 * @param {HTMLElement} body
 * @returns {HTMLElement[]}
 */
function blockBoxes(body) {
    return Array.from(
        /** @type {HTMLCollectionOf<HTMLElement>} */ (body.children)
    )
}

// Compact recent-blocks toggle (enabled by default)
let rbCompact = true

export function start() {
    // The recent blocks are one tab stop: the block focused last,
    // until a redraw makes it the newest again, unless the focus is
    // on a block then. The arrow keys, Home and End move the focus
    // among them; with a modifier they are the browser's.
    $('recent-blocks-body').addEventListener('keydown', function (e) {
        if (e.altKey || e.ctrlKey || e.metaKey) return
        if (!(e.target instanceof HTMLElement)) return
        const boxes = blockBoxes(this)
        const at = boxes.indexOf(e.target)
        if (at < 0) return
        let to
        switch (e.key) {
            case 'ArrowLeft':
                to = Math.max(0, at - 1)
                break
            case 'ArrowRight':
                to = Math.min(boxes.length - 1, at + 1)
                break
            case 'Home':
                to = 0
                break
            case 'End':
                to = boxes.length - 1
                break
            default:
                return
        }
        e.preventDefault()
        present(boxes[to]).focus()
    })
    $('recent-blocks-body').addEventListener('focusin', function (e) {
        for (const box of blockBoxes(this))
            box.tabIndex = box === e.target ? 0 : -1
    })
    const rbCompactCb = /** @type {HTMLInputElement} */ ($('rb-compact'))
    const rbStored = storageGet('rb-compact')
    if (rbStored !== null) rbCompact = rbStored === 'true'
    rbCompactCb.checked = rbCompact

    rbCompactCb.addEventListener('change', function () {
        rbCompact = rbCompactCb.checked
        storageSet('rb-compact', rbCompact ? 'true' : 'false')
        renderRecentBlocks(live.lastRecentBlocks)
    })
}
