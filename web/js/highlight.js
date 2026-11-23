// Highlighting across the page: pointing at an element with
// data-highlight marks every other element carrying its values.

/** @type {string[] | null} */
let currentHighlight = null
// highlightedCell is the element the highlight started from, null
// when none.
/** @type {Element | null} */
let highlightedCell = null

/* Highlighting: hover any element with data-highlight → collect all highlight
   values from the containing row (if in a table) or just the element itself →
   highlight all matching elements across the entire page */
function setupHighlight() {
    document.addEventListener(
        'mouseenter',
        function (e) {
            if (!(e.target instanceof Element)) return
            const cell = e.target.closest('[data-highlight]')
            if (cell) highlightFrom(cell)
        },
        true
    )
    // A pointer moving off the highlighted cell clears it, also
    // when the browser sends no mouseleave: none comes for a cell
    // a redraw put under a still pointer.
    document.addEventListener(
        'mousemove',
        function (e) {
            if (!highlightedCell || !(e.target instanceof Element)) return
            if (!highlightedCell.contains(e.target)) clearHighlight()
        },
        { passive: true }
    )
    // Moving between a cell's own parts keeps its highlight.
    document.addEventListener(
        'mouseleave',
        function (e) {
            if (!(e.target instanceof Element)) return
            const cell = e.target.closest('[data-highlight]')
            if (!cell) return
            if (
                e.relatedTarget instanceof Node &&
                cell.contains(e.relatedTarget)
            )
                return
            clearHighlight()
        },
        true
    )
}
// highlightFrom highlights what cell, an element with
// data-highlight, names: cell and the cells of its row with it
// as the source, every other element of the page carrying one
// of their values, and its row.
/** @param {Element} cell */
function highlightFrom(cell) {
    clearHighlight()
    // The values: those of the row's cells in a table, else the
    // element's own.
    const row = cell.closest('tr')
    const sourceEls = row
        ? Array.from(row.querySelectorAll('[data-highlight]'))
        : [cell]
    const vals = []
    for (const el of sourceEls) {
        const v = el.getAttribute('data-highlight')
        if (v) vals.push(v)
    }
    if (vals.length === 0) return
    currentHighlight = vals
    highlightedCell = cell

    if (row) row.classList.add('hl-source')
    for (const el of sourceEls) el.classList.add('hl-source')

    // Every other element of the page carrying one of the
    // values, and its row.
    for (const v of vals) {
        const sel = '[data-highlight="' + CSS.escape(v) + '"]'
        for (const el of document.querySelectorAll(sel)) {
            if (sourceEls.includes(el)) continue
            el.classList.add('hl')
            const mr = el.closest('tr')
            if (mr && mr !== row) mr.classList.add('hl')
        }
    }
}
// refreshHighlight follows a redraw, the pointer on at: the
// elements of the highlight the pointer is still on are
// highlighted again, the redrawn ones with them; a pointer no
// longer on one clears it, as a removed element sends no
// mouseleave.
/** @param {Element | null} at */
export function refreshHighlight(at) {
    if (!currentHighlight) return
    const cell = at ? at.closest('[data-highlight]') : null
    if (cell) highlightFrom(cell)
    else clearHighlight()
}
function clearHighlight() {
    if (!currentHighlight) return
    const all = document.querySelectorAll('.hl, .hl-source')
    for (const el of all) {
        el.classList.remove('hl')
        el.classList.remove('hl-source')
    }
    currentHighlight = null
    highlightedCell = null
}

export function start() {
    setupHighlight()
}
