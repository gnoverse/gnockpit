// The page's popover: one element showing the text or the rows of the
// element it describes. Its state is set up on import, so hidePopover
// works from any feature's start, whether the popover's own start ran or
// not.

import { $, clearChildren, mkEl } from './dom.js'

// The open popover follows its target: it hides once the page
// takes the target out or stops showing it, and shows the
// target's new content when the page changes it in place.
/** @type {string | null} */
let shownSource = null
// Where the target was in the viewport, and the page's scroll,
// when its popover showed: the popover sits on the page.
/** @typedef {{ top: number, left: number, x: number, y: number }} PopoverPlace */
/** @type {PopoverPlace | null} */
let shownAt = null
const popoverWatch = new MutationObserver(function () {
    const el = activePopover
    if (!el) return
    if (!el.isConnected || !el.getClientRects().length) hidePopover()
    else if (popoverSource(el) !== shownSource) showPopover(el)
})
const POPOVER_WATCH = {
    subtree: true,
    childList: true,
    attributes: true,
    attributeFilter: [
        'class',
        'style',
        'hidden',
        'data-popover',
        'data-pop-rows',
    ],
}
/** @type {Element | null} */
let activePopover = null

/* Popover: hover on desktop, click/touch on mobile. One popover,
   two forms: text (data-popover) and rows (data-pop-rows, set by
   popRows). */
const hasHover = window.matchMedia('(hover: hover)').matches
const POP_TARGETS = '.info-btn, [data-popover], [data-pop-rows]'
const TAP_TARGETS = '.info-btn, .tap-popover, [data-pop-rows]'

// popRows gives el the rows form: an optional title, then rows of
// [swatch colour or null, label, value].
/** @typedef {[string | null, string, string]} PopRow */
/**
 * @param {Element} el
 * @param {string} title
 * @param {PopRow[]} rows
 */
export function popRows(el, title, rows) {
    el.removeAttribute('data-popover')
    el.setAttribute(
        'data-pop-rows',
        JSON.stringify({ t: title || '', r: rows })
    )
}

// fillPopover renders el's popover into pop as DOM text nodes.
/**
 * @param {HTMLElement} pop
 * @param {Element} el
 */
function fillPopover(pop, el) {
    clearChildren(pop)
    const rows = el.getAttribute('data-pop-rows')
    pop.classList.toggle('rows', !!rows)
    if (!rows) {
        pop.textContent = el.getAttribute('data-popover')
        return
    }
    // As popRows wrote them.
    /** @type {{ t: string, r: PopRow[] }} */
    const d = JSON.parse(rows)
    if (d.t) pop.appendChild(mkEl('div', d.t, 't'))
    const table = document.createElement('table')
    d.r.forEach(function (r) {
        const tr = document.createElement('tr')
        const label = mkEl('td', null, 'l')
        if (r[0]) {
            const sw = mkEl('span', null, 'sw')
            sw.style.background = r[0]
            label.appendChild(sw)
        }
        label.appendChild(document.createTextNode(r[1]))
        tr.appendChild(label)
        tr.appendChild(mkEl('td', r[2], 'v'))
        table.appendChild(tr)
    })
    pop.appendChild(table)
}

// showPopover opens el's popover below it, or above it when there
// is no room below, kept within the screen's width; the popover
// follows el from then on (popoverWatch).
/** @param {Element} el */
export function showPopover(el) {
    const pop = $('popover')
    fillPopover(pop, el)
    // Measured from the page's corner, not where the last one sat.
    pop.style.top = pop.style.left = '0px'
    pop.classList.add('visible')
    const rect = el.getBoundingClientRect()
    let top = rect.bottom + 4
    if (top + pop.offsetHeight > window.innerHeight)
        top = Math.max(4, rect.top - 4 - pop.offsetHeight)
    const left = Math.max(
        4,
        Math.min(rect.left, window.innerWidth - pop.offsetWidth - 8)
    )
    pop.style.top = top + window.scrollY + 'px'
    pop.style.left = left + window.scrollX + 'px'
    // The popover describes its target, and no other element.
    if (activePopover && activePopover !== el)
        activePopover.removeAttribute('aria-describedby')
    el.setAttribute('aria-describedby', 'popover')
    activePopover = el
    shownSource = popoverSource(el)
    shownAt = {
        top: rect.top,
        left: rect.left,
        x: window.scrollX,
        y: window.scrollY,
    }
    popoverWatch.observe(document.body, POPOVER_WATCH)
}
export function hidePopover() {
    $('popover').classList.remove('visible')
    if (activePopover) activePopover.removeAttribute('aria-describedby')
    activePopover = null
    popoverWatch.disconnect()
}

// popoverSource is what el's popover shows: its rows, or its text.
/**
 * @param {Element} el
 * @returns {string | null}
 */
function popoverSource(el) {
    return el.getAttribute('data-pop-rows') || el.getAttribute('data-popover')
}

export function start() {
    // A scroll that moves the target from under its popover, or the
    // page under a target that stays (in a dialog, or kept in view
    // as content shows above it): the popover of an element the
    // keyboard focused moves with it, any other hides. A scroll that
    // leaves both where they were, elsewhere or before the popover
    // showed, leaves it.
    document.addEventListener(
        'scroll',
        function () {
            const el = activePopover
            if (!el) return
            // showPopover sets shownAt along with activePopover.
            const at = /** @type {PopoverPlace} */ (shownAt)
            const r = el.getBoundingClientRect()
            if (
                r.top === at.top &&
                r.left === at.left &&
                window.scrollX === at.x &&
                window.scrollY === at.y
            )
                return
            if (el.matches(':focus-visible')) showPopover(el)
            else hidePopover()
        },
        { capture: true, passive: true }
    )

    document.addEventListener(
        'mouseenter',
        function (e) {
            if (!hasHover || !(e.target instanceof Element)) return
            const btn = e.target.closest(POP_TARGETS)
            if (btn) showPopover(btn)
        },
        true
    )
    // Moving between a target's own children keeps its popover open.
    document.addEventListener(
        'mouseleave',
        function (e) {
            if (!hasHover || !(e.target instanceof Element)) return
            const btn = e.target.closest(POP_TARGETS)
            if (!btn) return
            if (
                e.relatedTarget instanceof Node &&
                btn.contains(e.relatedTarget)
            )
                return
            hidePopover()
        },
        true
    )
    document.addEventListener('click', function (e) {
        if (!(e.target instanceof Element)) return
        const btn = e.target.closest(TAP_TARGETS)
        if (btn) {
            if (hasHover) return
            if (activePopover === btn) {
                hidePopover()
                return
            }
            showPopover(btn)
        } else {
            hidePopover()
        }
    })
    // An ⓘ the keyboard focuses shows its popover until the focus
    // leaves it. A pointer's focus does not: the pointer has its own
    // ways above.
    document.addEventListener('focusin', function (e) {
        if (
            e.target instanceof Element &&
            e.target.matches('.info-btn:focus-visible')
        )
            showPopover(e.target)
    })
    document.addEventListener('focusout', function (e) {
        if (e.target === activePopover) hidePopover()
    })
    // Escape hides an open popover. One the keyboard opened takes the
    // key for itself, so a dialog under it stays open; under one the
    // pointer opened, the key goes on, to close that dialog.
    document.addEventListener(
        'keydown',
        function (e) {
            if (e.key !== 'Escape' || !activePopover) return
            const keyboard = activePopover.matches(':focus-visible')
            hidePopover()
            if (keyboard) e.stopPropagation()
        },
        true
    )
}
