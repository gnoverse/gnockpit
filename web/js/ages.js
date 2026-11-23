// The ages the page shows, on the server's clock: the round's, the head
// block's and the chain's.

import { $, clearChildren, mkEl } from './dom.js'
import { ageText } from './format.js'
import { renderRoundAge, roundAgeSec } from './network-state.js'
import { live } from './state.js'

// renderGenesisAge shows, as of serverNow, before genesis the
// countdown banner with the node's time; after it, the chain's
// age; and either beside the genesis time.
/** @param {number} serverNow ms since the epoch */
function renderGenesisAge(serverNow) {
    if (!live.genesisTimeMs) return
    const diff = Math.floor((live.genesisTimeMs - serverNow) / 1000)
    const gcEl = $('genesis-countdown')
    let ageStr
    if (diff > 0) {
        gcEl.style.display = ''
        const hh = Math.floor(diff / 3600)
        const mm = Math.floor((diff % 3600) / 60)
        const cdStr =
            (hh > 0 ? hh + 'h ' : '') +
            (mm > 0 ? mm + 'm ' : '') +
            (diff % 60) +
            's'
        $('gc-countdown').textContent = cdStr
        $('gc-nodetime').textContent =
            new Date(serverNow).toISOString().substring(11, 19) + 'Z'
        ageStr = 'starts in ' + cdStr
    } else {
        gcEl.style.display = 'none'
        ageStr = ageText(-diff) + ' ago'
    }
    // The genesis time with its age in parens, like the block time.
    if (live.genesisTimeStr) {
        const gtEl = $('c-gentime')
        clearChildren(gtEl)
        gtEl.appendChild(document.createTextNode(live.genesisTimeStr))
        gtEl.appendChild(mkEl('span', ' (' + ageStr + ')', 'ago'))
    }
}

// renderHeadAge writes, as of now, how long ago the head block
// was made, beside its time, and tags its height stuck while the
// round has run over 30 s. The height is err while stuck or at 0,
// ok otherwise.
/** @param {number} now ms since the epoch */
function renderHeadAge(now) {
    // The age's place comes with the status (live's handleStatus).
    const ago = document.querySelector('#d-time .ago')
    const status = live.lastStatus
    if (ago && status) {
        const made = Date.parse(status.sync_info.latest_block_time)
        const s = Math.max(0, Math.floor((now - made) / 1000))
        ago.textContent = ' (' + ageText(s) + ' ago)'
    }
    const block = $('d-block')
    const tag = block.querySelector('.tag-red')
    const stuck = !!live.lastStatus && roundAgeSec(now) > 30
    if (stuck && !tag) block.appendChild(mkEl('span', ' stuck', 'tag tag-red'))
    else if (!stuck && tag) tag.remove()
    if (live.lastStatus && live.lastStatus.sync_info) {
        const h = live.lastStatus.sync_info.latest_block_height
        block.className = stuck || h === '0' ? 'v err' : 'v ok'
    }
}

// renderAges writes the ages the page shows as of the server's
// clock, since the browser's may be off: with each message, and
// every second as time goes by.
export function renderAges() {
    const now = Date.now() + live.serverTimeOffset
    renderRoundAge(now)
    renderHeadAge(now)
    renderGenesisAge(now)
}

export function start() {
    setInterval(renderAges, 1000)
}
