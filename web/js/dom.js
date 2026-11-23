// Looking elements up and building them, and the flash of a value
// that changed.

// $ is the element of the page with id, an HTML element the page
// cannot work without: a missing one throws, naming it, and so does
// one that is no HTML element. A lookup whose absence the page
// handles calls document.getElementById.
/**
 * @param {string} id
 * @returns {HTMLElement}
 */
export function $(id) {
    const el = document.getElementById(id)
    if (!el) throw new Error('missing #' + id)
    if (!(el instanceof HTMLElement))
        throw new Error('#' + id + ' is no HTML element')
    return el
}
// $svg is the element of the page with id, an SVG element the page
// cannot work without: a missing one throws, naming it, and so does
// one that is no SVG element.
/**
 * @param {string} id
 * @returns {SVGElement}
 */
export function $svg(id) {
    const el = document.getElementById(id)
    if (!el) throw new Error('missing #' + id)
    if (!(el instanceof SVGElement))
        throw new Error('#' + id + ' is no SVG element')
    return el
}
// $in is the first element under root that selector matches, an
// HTML element the page cannot work without: a missing one throws,
// naming the selector, and so does one that is no HTML element.
/**
 * @param {ParentNode} root
 * @param {string} selector
 * @returns {HTMLElement}
 */
export function $in(root, selector) {
    const el = root.querySelector(selector)
    if (!el) throw new Error('missing ' + selector)
    if (!(el instanceof HTMLElement))
        throw new Error(selector + ' is no HTML element')
    return el
}
// scheduleFlash flashes el again from the start: the class comes
// off now and goes back on at the next frame, after one reflow
// has applied the removal for every element waiting. A frame
// alone does not always apply it, and the flash would not restart.
/** @type {HTMLElement[]} */
let flashPending = []
let flashRafId = 0
/** @param {HTMLElement} el */
function scheduleFlash(el) {
    el.classList.remove('changed')
    flashPending.push(el)
    if (flashRafId) return
    flashRafId = requestAnimationFrame(function () {
        const pending = flashPending
        flashPending = []
        flashRafId = 0
        void document.body.offsetWidth
        for (const p of pending) p.classList.add('changed')
    })
}
/**
 * @param {string} id
 * @param {string} text
 */
export function setText(id, text) {
    const el = $(id)
    if (el.textContent !== text) {
        el.textContent = text
        scheduleFlash(el)
    }
}
// setTextWithClass sets the element's text and class; a new
// text flashes when flash is set. A running flash goes on: the
// class is back before the browser applies the new class list.
/**
 * @param {string} id
 * @param {string} text
 * @param {string} cls
 * @param {boolean} flash
 */
export function setTextWithClass(id, text, cls, flash) {
    const el = $(id)
    const changed = el.textContent !== text
    const flashing = el.classList.contains('changed')
    el.textContent = text
    el.className = cls
    if (flashing) el.classList.add('changed')
    if (changed && flash) {
        scheduleFlash(el)
    }
}
/** @param {Node} el */
export function clearChildren(el) {
    while (el.firstChild) el.removeChild(el.firstChild)
}

// mkEl builds an element of tag, with its text, its class and its
// attributes when given.
/**
 * @template {keyof HTMLElementTagNameMap} K
 * @param {K} tag
 * @param {string | null} [text]
 * @param {string | null} [cls]
 * @param {Record<string, string>} [attrs]
 * @returns {HTMLElementTagNameMap[K]}
 */
export function mkEl(tag, text, cls, attrs) {
    const el = document.createElement(tag)
    if (text !== undefined && text !== null) el.textContent = text
    if (cls) el.className = cls
    if (attrs) {
        for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v)
    }
    return el
}

// newTabLink is a link to href, of class cls, opening in a new
// tab that cannot reach the dashboard: the pages it links are
// others'.
/**
 * @param {string} href
 * @param {string} [cls]
 * @returns {HTMLAnchorElement}
 */
export function newTabLink(href, cls) {
    return mkEl('a', null, cls, {
        href: href,
        target: '_blank',
        rel: 'noopener',
    })
}

// mkLink is a newTabLink reading text, with an arrow that marks it
// as leaving the page.
/**
 * @param {string} text
 * @param {string} href
 * @param {string} [cls]
 * @returns {HTMLAnchorElement}
 */
export function mkLink(text, href, cls) {
    const a = newTabLink(href, cls)
    a.textContent = text
    a.appendChild(mkEl('span', ' \u2197', 'ext-arrow'))
    return a
}

// SVG_NS is the namespace of the SVG elements the page builds.
export const SVG_NS = 'http://www.w3.org/2000/svg'
