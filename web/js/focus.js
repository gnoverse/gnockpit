// Where the keyboard's focus goes: kept inside an open dialog, and on
// its control through a redraw; and a box scrolled to show an element.

// keepFocusIn keeps Tab and Shift+Tab inside a dialog: the focus
// wraps from its last control to its first and back, and comes
// back to it from wherever it went.
/**
 * @param {Element} dialog
 * @param {KeyboardEvent} e
 */
export function keepFocusIn(dialog, e) {
    const f = Array.from(
        dialog.querySelectorAll(
            'a[href], button:not(:disabled), input, [tabindex="0"]'
        )
    ).filter(focusable)
    const first = f[0]
    const last = f[f.length - 1]
    if (!first || !last) return
    const at = document.activeElement
    if (!dialog.contains(at)) {
        ;(e.shiftKey ? last : first).focus()
        e.preventDefault()
    } else if (e.shiftKey && at === first) {
        last.focus()
        e.preventDefault()
    } else if (!e.shiftKey && at === last) {
        first.focus()
        e.preventDefault()
    }
}

// focusPlaceIn notes where the focus is in box, for a redraw of box
// to give it back with focusPlace: the data-key of the element
// around the focus that carries one, the part of that element
// the focus is in (a row's cell; -1 for the element itself), and
// the focused control's rank among that part's controls. null
// when the focus is elsewhere.
/**
 * @typedef {object} FocusPlace
 * @property {Element} box
 * @property {string} key
 * @property {number} part
 * @property {number} rank
 */
/**
 * @param {Element} box
 * @returns {FocusPlace | null}
 */
export function focusPlaceIn(box) {
    const at = document.activeElement
    if (!at || !box.contains(at)) return null
    const keyed = at.closest('[data-key]')
    if (!(keyed instanceof HTMLElement) || !box.contains(keyed)) return null
    const parts = Array.from(keyed.children)
    const part = parts.findIndex(function (c) {
        return c.contains(at)
    })
    return {
        box: box,
        // keyed matches [data-key].
        key: /** @type {string} */ (keyed.dataset.key),
        part: part,
        rank: focusableIn((part < 0 ? null : parts[part]) ?? keyed).indexOf(at),
    }
}
// focusPlace focuses the control at place, a place focusPlaceIn
// noted, when the redrawn box still has it; the page does not
// scroll to it.
/** @param {FocusPlace | null} place */
export function focusPlace(place) {
    if (!place) return
    const keyed = place.box.querySelector(
        '[data-key="' + CSS.escape(place.key) + '"]'
    )
    if (!keyed) return
    const part = place.part < 0 ? keyed : keyed.children[place.part]
    const el = part && focusableIn(part)[place.rank]
    if (el && focusable(el)) el.focus({ preventScroll: true })
}
// focusableIn lists el and the controls in it that take the focus,
// in document order.
/**
 * @param {Element} el
 * @returns {Element[]}
 */
function focusableIn(el) {
    const sel = 'a[href], button, input, select, [tabindex]'
    const list = Array.from(el.querySelectorAll(sel))
    if (el.matches(sel)) list.unshift(el)
    return list
}
// focusable tells an element that takes the focus, an HTML or an SVG
// one, from others.
/**
 * @param {Element} el
 * @returns {el is HTMLElement | SVGElement}
 */
export function focusable(el) {
    return el instanceof HTMLElement || el instanceof SVGElement
}

// scrollIntoBox scrolls box, and no box around it, by the least
// distance that shows el, a box's descendant, inside box's
// scrollport: its border box less its borders.
/**
 * @param {Element} el
 * @param {Element} box
 */
export function scrollIntoBox(el, box) {
    const r = el.getBoundingClientRect()
    const top = box.getBoundingClientRect().top + box.clientTop
    const bottom = top + box.clientHeight
    const by =
        r.top < top ? r.top - top : r.bottom > bottom ? r.bottom - bottom : 0
    if (by) box.scrollBy({ top: by, behavior: 'smooth' })
}
