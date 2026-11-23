// Header link buttons (left of Alerts), configured via --link /
// --status-link. Status links show a dot polled from BetterStack.

/** @import { Link, LinksAnswer, StatusLinkOut } from '../types/server.js' */

import { $, clearChildren, mkEl, newTabLink } from './dom.js'
import { boundedRead } from './read.js'

// headerLinkDotColor is the colour of a status link's dot in state.
/**
 * @param {string} state
 * @returns {string}
 */
function headerLinkDotColor(state) {
    return state === 'operational'
        ? 'var(--green)'
        : state === 'downtime'
          ? 'var(--red)'
          : state === 'degraded' || state === 'maintenance'
            ? 'var(--yellow)'
            : 'var(--fg2)'
}
/** @param {LinksAnswer} d */
function renderHeaderLinks(d) {
    const box = $('header-links')
    clearChildren(box)
    /**
     * @param {Link | StatusLinkOut} l
     * @returns {HTMLAnchorElement}
     */
    function mkLinkBtn(l) {
        return newTabLink(l.url, 'header-link')
    }
    ;(d.links || []).forEach(function (l) {
        const a = mkLinkBtn(l)
        a.textContent = l.title
        box.appendChild(a)
    })
    ;(d.statusLinks || []).forEach(function (l) {
        const a = mkLinkBtn(l)
        const dot = mkEl('span', '', 'dot')
        dot.style.background = headerLinkDotColor(l.state)
        dot.setAttribute('data-popover', l.state)
        a.appendChild(dot)
        a.appendChild(document.createTextNode(l.title))
        box.appendChild(a)
    })
}
// LINKS_READ_BOUND_MS bounds a read of the links: one without an
// answer by then is given up, as one that failed.
const LINKS_READ_BOUND_MS = 10000
function loadHeaderLinks() {
    boundedRead(LINKS_READ_BOUND_MS, async function (signal) {
        const r = await fetch('/api/links', { signal })
        /** @type {LinksAnswer} */
        const d = await r.json()
        return d
    })
        .then(renderHeaderLinks)
        .catch(function () {
            // The links stay as they show until a read answers.
        })
}

export function start() {
    loadHeaderLinks()
    setInterval(loadHeaderLinks, 30000)
}
