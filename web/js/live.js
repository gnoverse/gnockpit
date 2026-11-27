// The dashboard's live data: the /ws connection, whose messages fill the
// shared state and redraw the cards.

/** @import { CheckData, WsMsg } from '../types/server.js' */
/** @import { Peer, SigningStats, Status, VotesReport } from '../types/node.js' */

import { renderAges } from './ages.js'
import { showChainName } from './boot.js'
import { compareSortValues } from './collate.js'
import { $, clearChildren, mkEl, setText } from './dom.js'
import { b64toHex } from './format.js'
import { renderMap } from './map.js'
import { knownValAddrs, validatorName } from './names.js'
import { scheduleRenderNetwork } from './network-state.js'
import { popRows } from './popover.js'
import { renderRecentBlocks } from './recent-blocks.js'
import { live } from './state.js'

// setHashWithHeight shows hash b64val, in hex, and the height it is
// of in the element with id; '-' without a hash.
/**
 * @param {string} id
 * @param {string} b64val
 * @param {string} height
 */
function setHashWithHeight(id, b64val, height) {
    const el = $(id)
    if (!b64val || b64val === '-') {
        el.textContent = '-'
        el.className = 'v'
        el.removeAttribute('data-pop-rows')
        el.removeAttribute('data-copy')
        return
    }
    const hex = b64toHex(b64val)
    clearChildren(el)
    const hexSpan = document.createElement('span')
    hexSpan.className = 'hash-hex'
    hexSpan.textContent = hex
    el.appendChild(hexSpan)
    const num = document.createElement('span')
    num.className = 'hash-h'
    num.textContent = '(' + height + ')'
    el.appendChild(num)
    el.setAttribute('data-copy', hex)
    el.className = 'v hash ok'
    popRows(el, '', [
        [null, 'hex', hex],
        [null, 'base64', b64val],
    ])
}

/** @param {Status} d */
function handleStatus(d) {
    live.lastStatus = d
    if (d.sync_info) {
        const h = d.sync_info.latest_block_height
        // A click copies the height and the time alone,
        // without the stuck tag or the age.
        const dBlock = $('d-block')
        clearChildren(dBlock)
        dBlock.appendChild(document.createTextNode(h))
        dBlock.setAttribute('data-copy', h)

        const bt = d.sync_info.latest_block_time || ''
        const timeEl = $('d-time')
        clearChildren(timeEl)
        if (h === '0' || bt.indexOf('1970') === 0) {
            timeEl.textContent = '-'
            timeEl.removeAttribute('data-copy')
        } else {
            timeEl.appendChild(document.createTextNode(bt))
            timeEl.setAttribute('data-copy', bt)
            // How long ago: renderAges writes it.
            if (!Number.isNaN(Date.parse(bt)))
                timeEl.appendChild(mkEl('span', '', 'ago'))
        }
        timeEl.className = 'v'
    }
    if (d.node_info) {
        const network = d.node_info.network || '-'
        // The chain-id row always shows the real chain-id.
        setText('d-network', network)
        live.chainNetwork = network
        showChainName()
    }
}

/** @param {SigningStats} d */
function handleSigning(d) {
    if (!d) return
    // The active count's band: ok when all are active, warn above
    // ⅔ of them, err otherwise; the compact line shows it too.
    const activeCount = d.active_count + '/' + d.total_count
    const activeBand =
        d.active_count >= d.total_count
            ? 'ok'
            : d.active_count > (d.total_count * 2) / 3
              ? 'warn'
              : 'err'
    const activeEl = $('ns-active')
    setText('ns-active', activeCount)
    activeEl.className = 'v ' + activeBand
    if (d.avg_block_ms > 0) {
        setText('ns-avgblock', (d.avg_block_ms / 1000).toFixed(1) + 's')
    }
    const avgStr =
        d.avg_block_ms > 0 ? (d.avg_block_ms / 1000).toFixed(1) + 's' : '-'
    $('ns-active-compact').replaceChildren(
        mkEl('span', activeCount, activeBand),
        ' · ' + avgStr + ' avg'
    )

    // Recent blocks
    if (d.recent_blocks && d.recent_blocks.length > 0) {
        live.lastRecentBlocks = d.recent_blocks
        renderRecentBlocks(d.recent_blocks)
    }
}

/** @param {CheckData} d */
function handleChecks(d) {
    live.hideSources = !!d.hide_sources
    if (d.apphash_last && live.lastStatus && live.lastStatus.sync_info) {
        const h = live.lastStatus.sync_info.latest_block_height
        setHashWithHeight('d-apphash-last', d.apphash_last, h)
    }
    if (d.system && d.system.node_time) {
        const serverMs = new Date(d.system.node_time).getTime()
        live.serverTimeOffset = serverMs - Date.now()
        live.serverTimeKnown = true
        const driftSec = Math.abs(Math.round(live.serverTimeOffset / 1000))
        const driftEl = $('gc-drift')
        if (driftSec > 2) {
            driftEl.style.display = ''
            driftEl.style.color = driftSec > 10 ? 'var(--red)' : 'var(--yellow)'
            driftEl.textContent =
                '\u26a0 clock drift: ' +
                driftSec +
                's between your browser and the node \u2014 consider ntpdate'
        } else {
            driftEl.style.display = 'none'
        }
    }
    if (d.system && d.system.genesis_time) {
        live.genesisTimeMs = new Date(d.system.genesis_time).getTime()
        live.genesisTimeStr = d.system.genesis_time
        // A click copies the genesis time without its age.
        $('c-gentime').setAttribute('data-copy', live.genesisTimeStr)
        $('gc-time').textContent = d.system.genesis_time
    }
}

// setPeers keeps the peers in name order, nameless ones last:
// the order the Peers table keeps among peers alike in its
// sorted column.
/** @param {Peer[] | null} d */
function setPeers(d) {
    live.lastPeers = d
    if (live.lastPeers)
        live.lastPeers.sort(function (a, b) {
            return compareSortValues(
                a.moniker || null,
                b.moniker || null,
                true,
                true
            )
        })
    renderMap(live.lastPeers, live.declared)
}

/** @param {VotesReport | null} d */
function setVotes(d) {
    live.lastVotes = d
    // The names of the validators named otherwise than by their
    // address, by address.
    for (const v of (d && d.validators) || []) {
        const { name } = validatorName(v.name)
        if (v.address && name && name !== v.address)
            knownValAddrs.set(v.address, name)
    }
}

function connect() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const ws = new WebSocket(proto + '//' + location.host + '/ws')

    ws.addEventListener('open', function () {
        $('error-banner').style.display = 'none'
    })

    ws.addEventListener('message', function (e) {
        /** @type {WsMsg} */
        const msg = JSON.parse(e.data)
        switch (msg.type) {
            case 'update': // Batched periodic update (all fields in one message)
            case 'snapshot': // Initial full state on connect
                // The peers and the votes are null while the
                // sources have none: the tables and the map
                // empty. Without a status the last head stays.
                if (msg.data.status) handleStatus(msg.data.status)
                // The map and the tables draw the declared locations
                // beside the peers.
                live.declared = msg.data.declared
                setPeers(msg.data.peers)
                setVotes(msg.data.votes)
                live.dataSeen = true
                scheduleRenderNetwork()
                if (msg.data.checks) handleChecks(msg.data.checks)
                if (msg.data.signing) handleSigning(msg.data.signing)
                renderAges()
                break
        }
    })

    // An error is always followed by close, which reconnects.
    ws.addEventListener('close', function () {
        const banner = $('error-banner')
        banner.textContent = 'WebSocket connection lost, reconnecting...'
        banner.style.display = 'block'
        setTimeout(connect, 2000)
    })
}

export function start() {
    connect()
}
