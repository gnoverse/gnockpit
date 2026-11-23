// Country/provider cross-highlight: hovering any element tagged
// data-cc or data-prov — a map cluster, a side-list row, or a table
// cell — highlights every element of that value; if any map cluster
// matches, the rest of the map dims to make the match pop. Kept
// separate from the generic data-highlight system so it never bleeds
// into validator/address row highlighting.

import { $, $svg } from './dom.js'
import { scrollIntoBox } from './focus.js'

// countryHighlight is the selector of the highlighted country or
// provider, '' when none.
let countryHighlight = ''
function clearCountryHighlight() {
    countryHighlight = ''
    const hl = document.querySelectorAll('.cc-hl')
    for (const el of hl) el.classList.remove('cc-hl')
    $svg('map-points').classList.remove('dim-others')
}
// highlightCountry highlights every element sel matches, and no
// other, and dims the rest of the map while a map cluster is among
// them.
/** @param {string} sel */
function highlightCountry(sel) {
    clearCountryHighlight()
    countryHighlight = sel
    for (const el of document.querySelectorAll(sel)) el.classList.add('cc-hl')
    const mp = $svg('map-points')
    mp.classList.toggle('dim-others', mp.querySelector(sel) !== null)
}
// refreshCountryHighlight follows a redraw, the pointer on at: the
// redrawn elements of the country or provider the pointer is
// still on are highlighted like the ones they replace; a pointer
// no longer on it clears it, as a removed element sends no
// mouseleave.
/** @param {Element | null} at */
export function refreshCountryHighlight(at) {
    if (!countryHighlight) return
    const el = at ? at.closest('[data-cc],[data-prov]') : null
    if (el && el.matches(countryHighlight)) highlightCountry(countryHighlight)
    else clearCountryHighlight()
}

export function start() {
    // A pointer moving off the highlighted country or provider clears
    // it, also when the browser sends no mouseleave: WebKit sends none
    // for an element a redraw put under a still pointer.
    document.addEventListener(
        'mousemove',
        function (e) {
            if (!countryHighlight || !(e.target instanceof Element)) return
            const el = e.target.closest('[data-cc],[data-prov]')
            if (!el || !el.matches(countryHighlight)) clearCountryHighlight()
        },
        { passive: true }
    )
    document.addEventListener(
        'mouseenter',
        function (e) {
            if (!(e.target instanceof Element)) return
            const el = e.target.closest('[data-cc],[data-prov]')
            if (!el) return
            const attr = el.hasAttribute('data-cc') ? 'data-cc' : 'data-prov'
            const val = el.getAttribute(attr)
            if (!val) return
            const sel = '[' + attr + '="' + CSS.escape(val) + '"]'
            // Map clusters carry the attribute of the active list mode.
            highlightCountry(sel)
            // Scroll the side list to the hovered value only when the
            // hover starts on the map itself. Doing it for a table-cell
            // hover would jerk the page up to the map — highlight only.
            // Only the list scrolls: the page, and the popover the
            // cluster opened, stay where they are.
            if (el.classList.contains('map-dot')) {
                const row = document.querySelector('#map-country-list ' + sel)
                if (row) scrollIntoBox(row, $('map-country-list'))
            }
        },
        true
    )
    // Moving between the parts of a cluster, row or cell keeps its
    // highlight.
    document.addEventListener(
        'mouseleave',
        function (e) {
            if (!(e.target instanceof Element)) return
            const el = e.target.closest('[data-cc],[data-prov]')
            if (!el) return
            if (e.relatedTarget instanceof Node && el.contains(e.relatedTarget))
                return
            clearCountryHighlight()
        },
        true
    )
}
