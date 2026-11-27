// The validator page's building blocks, shared by the modules that
// build its sections: a section, and the ⓘ that says what a datum is.

import { mkEl } from './dom.js'

// info builds an ⓘ whose popover says what a datum is, where it comes
// from, and whether the ranking scores it.
/**
 * @param {string} what
 * @param {string} source
 * @param {string} scored
 * @returns {HTMLSpanElement}
 */
export function info(what, source, scored) {
    const i = mkEl('span', 'ⓘ', 'info-btn', { tabindex: '0' })
    i.setAttribute(
        'data-popover',
        'What: ' +
            what +
            '\nSource: ' +
            source +
            '\nScored: ' +
            (scored ? 'yes, ' + scored : 'no, shown for monitoring')
    )
    return i
}

// section is a section of the page: its title, then its ⓘ.
/**
 * @param {string} key
 * @param {string} name
 * @param {HTMLElement} i
 * @returns {HTMLDivElement}
 */
export function section(key, name, i) {
    const sec = mkEl('div', null, 'val-sec', {
        'data-section': key,
    })
    const h = mkEl('h3', name)
    h.appendChild(i)
    sec.appendChild(h)
    return sec
}
