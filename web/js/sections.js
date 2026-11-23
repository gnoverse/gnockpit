// The page's sections fold from their titles, and stay as the viewer
// left them.

import { $in } from './dom.js'
import { storageObject, storageSet } from './storage.js'

export function start() {
    // isMobile is a page-load snapshot — intentionally not reactive to resize.
    // It asks the stylesheet's own query, as network-state.js does.
    const isMobile = window.matchMedia('(max-width: 768px)').matches
    const storageKey = isMobile
        ? 'gnockpit-collapsed-mobile'
        : 'gnockpit-collapsed'
    const collapsed = storageObject(storageKey)

    const headers = document.querySelectorAll('h2[data-section]')
    for (const h of headers) {
        // The button in the title folds the section; the title
        // keeps the class the arrow and the section body follow.
        const button = $in(h, 'button')
        // The headers' selector names the attribute.
        const key = /** @type {string} */ (h.getAttribute('data-section'))
        /** @param {boolean} folded */
        const setFolded = function (folded) {
            h.classList.toggle('collapsed', folded)
            button.setAttribute('aria-expanded', String(!folded))
        }
        setFolded(!!collapsed[key])
        button.addEventListener('click', function () {
            const folded = !h.classList.contains('collapsed')
            setFolded(folded)
            if (folded) collapsed[key] = true
            else delete collapsed[key]
            storageSet(storageKey, JSON.stringify(collapsed))
        })
    }
}
