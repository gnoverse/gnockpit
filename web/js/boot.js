// The boot status, read from /api/boot for as long as the page is open:
// the chain's name for the page's title, the panel shown while no
// source answers, and whether the server has the ranking on.

/** @import { BootStatus } from '../types/server.js' */

import { $ } from './dom.js'
import { boundedRead } from './read.js'
import { live } from './state.js'
import { offerRanking } from './timeline.js'

// showChainName titles the page with the chain's display name: the
// --chain-name override if set, otherwise the chain-id. It waits
// for /api/boot's first answer, which tells whether there is an
// override, so the title never shows the chain-id in its place.
export function showChainName() {
    if (live.chainNameOverride === null) return
    const chainDisplay = live.chainNameOverride || live.chainNetwork
    if (!chainDisplay || chainDisplay === '-') return
    const title = 'Gnockpit ' + chainDisplay
    $('page-title').textContent = title
    document.title = title
    // Update PWA theme-color meta to match the chain's icon
    // background color. djb2 hash mirrors Go's icon.ChainColor
    // (fed the same display name) so the two stay in sync.
    const tcMeta = /** @type {HTMLMetaElement} */ ($('theme-color-meta'))
    let h = 5381
    for (let i = 0; i < chainDisplay.length; i++) {
        h = (Math.imul(h, 33) ^ chainDisplay.charCodeAt(i)) >>> 0
    }
    tcMeta.content = 'hsl(' + (h % 360) + ',55%,26%)'
}

// The boot status is read for as long as the page is open: the
// panel doubles as the warning that every source is unreachable.
// A read is given up after 4 s, before the next tick reads again.
function pollBoot() {
    boundedRead(4000, async function (signal) {
        const r = await fetch('/api/boot', { signal })
        if (!r.ok) throw new Error('/api/boot: HTTP ' + r.status)
        /** @type {BootStatus} */
        const status = await r.json()
        return status
    })
        .then(function (d) {
            live.chainNameOverride =
                typeof d.chain_name === 'string' ? d.chain_name : ''
            showChainName()
            offerRanking(d.ranking === true)
            const panel = $('boot-panel')
            if (d.rpc) {
                panel.style.display = 'none'
                return
            }
            panel.style.display = 'block'
            $('boot-info').textContent = 'waiting...'
        })
        .catch(function () {
            // The panel keeps its state until a read answers, as
            // the next tick reads again. The title stops waiting
            // for the --chain-name override and shows the chain-id.
            if (live.chainNameOverride === null) {
                live.chainNameOverride = ''
                showChainName()
            }
        })
}

export function start() {
    pollBoot()
    setInterval(pollBoot, 5000)
}
