// The Validators and Peers cards: each a table sorted by the column the
// viewer picks, and a card view for narrow screens.

/** @import { Peer, VoteInfo } from '../types/node.js' */
/** @import { PopRow } from './popover.js' */

import { compareSortValues } from './collate.js'
import { copyNotify } from './copy.js'
import { countryFlag, countryName } from './countries.js'
import { refreshCountryHighlight } from './country-highlight.js'
import { $, $in, clearChildren, mkEl, mkLink, newTabLink } from './dom.js'
import { focusPlace, focusPlaceIn } from './focus.js'
import {
    formatVP,
    powerOf,
    scoreBand,
    voteDelayClass,
    voteDelayText,
    votingPower,
} from './format.js'
import { refreshHighlight } from './highlight.js'
import {
    shortAddr,
    shortenAddressName,
    valAddrToName,
    validatorName,
    valLink,
} from './names.js'
import { pointedElement } from './pointer.js'
import { popRows } from './popover.js'
import { live } from './state.js'
import { storageGet, storageSet } from './storage.js'

/**
 * A table cell: its text, a link to href when given; its class, style,
 * highlight and copy values; tags after the text; the rows of an ⓘ
 * popover after them.
 * @typedef {object} CellDef
 * @property {string} text
 * @property {string} [href]
 * @property {string} [className]
 * @property {string} [style]
 * @property {string} [highlight]
 * @property {string} [copy]
 * @property {Array<{ text: string, cls: string }>} [tags]
 * @property {PopRow[] | undefined} [rows]
 */

// Sortable table columns — the validators and peers tables each keep
// their own sort key + direction (their columns differ).
let valSortKey = 'name'
let valSortAsc = true
let peerSortKey = 'name'
let peerSortAsc = true
// sortBy sorts th's table by th's column: ascending for a new
// column, the other way for the sorted one.
/** @param {Element} th */
function sortBy(th) {
    // The sortable headers' selector names the attribute.
    const key = /** @type {string} */ (th.getAttribute('data-sort'))
    const table = th.closest('table')
    const isPeer = !!(table && table.querySelector('#peers-body'))
    if (isPeer) {
        if (peerSortKey === key) peerSortAsc = !peerSortAsc
        else {
            peerSortKey = key
            peerSortAsc = true
        }
    } else {
        if (valSortKey === key) valSortAsc = !valSortAsc
        else {
            valSortKey = key
            valSortAsc = true
        }
    }
    updateSortHeaders()
    renderNodeLists()
}

// createCell builds a table cell from cellDef, or one reading text.
/**
 * @param {CellDef | string} cellDef
 * @returns {HTMLTableCellElement}
 */
function createCell(cellDef) {
    const td = document.createElement('td')
    if (
        typeof cellDef === 'object' &&
        cellDef !== null &&
        cellDef.text !== undefined
    ) {
        if (cellDef.href) {
            td.appendChild(
                mkLink(cellDef.text, cellDef.href, cellDef.className || '')
            )
        } else {
            td.textContent = cellDef.text
            if (cellDef.className) td.className = cellDef.className
        }
        if (cellDef.style) td.setAttribute('style', cellDef.style)
        if (cellDef.highlight)
            td.setAttribute('data-highlight', cellDef.highlight)
        if (cellDef.copy) td.setAttribute('data-copy', cellDef.copy)
        if (cellDef.tags) {
            for (const tag of cellDef.tags)
                td.appendChild(mkEl('span', tag.text, 'tag ' + tag.cls))
        }
        if (cellDef.rows) {
            const btn = mkEl('span', ' \u24d8', 'info-btn', {
                tabindex: '0',
            })
            popRows(btn, '', cellDef.rows)
            td.appendChild(btn)
        }
    } else {
        td.textContent = String(cellDef)
    }
    return td
}

// countryCell builds a peers-table cell showing the peer's country:
// flag + full name, collapsing to the ISO code as the column narrows
// (CSS container query). data-cc cross-links it to the network map.
/**
 * @param {string} code
 * @returns {HTMLTableCellElement}
 */
function countryCell(code) {
    const td = document.createElement('td')
    td.className = 'country-cell'
    if (!code) {
        td.textContent = '—'
        return td
    }
    td.setAttribute('data-cc', code)
    td.setAttribute('data-copy', countryName(code))
    td.setAttribute('data-popover', countryName(code))
    td.classList.add('tap-popover')
    const inner = mkEl('span', null, 'cc-inner')
    inner.appendChild(mkEl('span', countryFlag(code), 'cc-flag'))
    inner.appendChild(mkEl('span', countryName(code), 'cc-name'))
    inner.appendChild(mkEl('span', code, 'cc-code'))
    td.appendChild(inner)
    return td
}

// countryShowsCode reports whether a table body's country cells are
// currently collapsed to the ISO code (container query on a narrow
// column) rather than the full name, so sorting can match the display.
/**
 * @param {Element} body
 * @returns {boolean}
 */
function countryShowsCode(body) {
    const el = body.querySelector('.cc-name')
    return !!(el && getComputedStyle(el).display === 'none')
}

// providerCell builds a cell showing the guessed cloud provider with
// an info icon whose popover reveals the IP it was guessed from.
/**
 * @param {string} provider
 * @param {string} ip
 * @returns {HTMLTableCellElement}
 */
function providerCell(provider, ip) {
    if (!provider && !ip) {
        const empty = document.createElement('td')
        empty.textContent = '—'
        return empty
    }
    const td = createCell({
        text: provider || 'unknown',
        style: 'font-size:11px;color:var(--fg2)',
        rows: ip ? [[null, 'guessed from IP', ip]] : undefined,
        copy: provider || ip,
    })
    if (provider) td.setAttribute('data-prov', provider)
    return td
}

// emptyRow is the row of a table with nothing to list: text in
// one cell spanning its columns.
/**
 * @param {string} text
 * @param {number} columns
 * @returns {HTMLTableRowElement}
 */
function emptyRow(text, columns) {
    const td = mkEl('td', text, 'full-role')
    td.colSpan = columns
    const tr = document.createElement('tr')
    tr.appendChild(td)
    return tr
}

// voteDelayNode renders a validator's live vote delay, coloured;
// "-" when the window holds none of its precommits.
/**
 * @param {number | null} ms
 * @returns {Node}
 */
function voteDelayNode(ms) {
    if (typeof ms !== 'number') return document.createTextNode('-')
    return mkEl('span', voteDelayText(ms), voteDelayClass(ms))
}

// proposalsNode renders missed / expected proposals, the missed
// count coloured by the proposals band when any were expected.
/**
 * @param {number} missed
 * @param {number} expected
 * @returns {HTMLSpanElement}
 */
function proposalsNode(missed, expected) {
    missed = missed || 0
    expected = expected || 0
    const node = mkEl('span')
    node.appendChild(
        mkEl(
            'span',
            String(missed),
            expected ? scoreBand.proposals((missed / expected) * 100) : null
        )
    )
    node.appendChild(document.createTextNode(' / ' + expected))
    return node
}

// missedSplitRows splits a validator's misses over the last 100
// blocks into absent and other block, with its nil votes, which
// count as signed.
/**
 * @param {VoteInfo} v
 * @returns {PopRow[]}
 */
function missedSplitRows(v) {
    const other = v.other_block_100 || 0
    return [
        [null, 'missed', String(v.missed_100)],
        [null, 'absent', String(v.missed_100 - other)],
        [null, 'other block', String(other)],
        [null, 'nil votes', (v.nil_100 || 0) + ' (counted as signed)'],
    ]
}

// midTruncCell builds a peers/validators cell that shows the full text
// when the column is wide enough and truncates in the middle
// (start…end) when it isn't. opts: rows (its popover), copy,
// highlight.
/**
 * @param {string} text
 * @param {{ rows?: PopRow[] | undefined, copy?: string, highlight?: string }} [opts]
 * @returns {HTMLTableCellElement}
 */
function midTruncCell(text, opts) {
    opts = opts || {}
    text = text || ''
    const td = document.createElement('td')
    td.setAttribute('style', 'font-size:11px;color:var(--fg2)')
    const wrap = mkEl('span', null, 'mid-trunc')
    const tail = 7
    if (text.length > tail) {
        wrap.appendChild(mkEl('span', text.slice(0, -tail), 'mt-start'))
        wrap.appendChild(mkEl('span', text.slice(-tail), 'mt-end'))
    } else {
        wrap.appendChild(mkEl('span', text, 'mt-start'))
    }
    td.appendChild(wrap)
    if (opts.rows) popRows(td, '', opts.rows)
    if (opts.copy) td.setAttribute('data-copy', opts.copy)
    if (opts.highlight) td.setAttribute('data-highlight', opts.highlight)
    return td
}

// nodeListsFingerprint sums up what the Validators and Peers
// tables and cards show, their order included: a message that
// changes none of it leaves them as they are, with an open popover
// or the focus in them.
let lastNodeListsFingerprint = ''
/** @returns {string} */
function nodeListsFingerprint() {
    const vals = live.lastVotes ? live.lastVotes.validators || [] : []
    return JSON.stringify([
        valSortKey,
        valSortAsc,
        peerSortKey,
        peerSortAsc,
        live.hideSources,
        live.lastVotes ? live.lastVotes.missed_since : null,
        vals.map(function (v) {
            return [
                v.name,
                v.address,
                v.pub_key,
                v.voting_power,
                v.inactive,
                v.missed_100,
                v.nil_100,
                v.other_block_100,
                v.missed_24h,
                v.vote_delay_ms,
                v.missed_proposals_24h,
                v.expected_proposals_24h,
            ]
        }),
        (live.lastPeers || []).map(function (p) {
            return [
                p.moniker,
                p.node_id,
                p.val_address,
                p.val_pubkey,
                p.remote_ip,
                p.external_address,
                p.country,
                p.provider,
                p.n_peers,
                p.rpc_url,
                p.height,
                p.catching_up,
                p.source,
            ]
        }),
    ])
}

// peerCountText writes peer p's own peer count: "-" without one,
// the server leaving out a count it did not read and a count of 0.
/**
 * @param {Peer} p
 * @returns {string}
 */
function peerCountText(p) {
    return p.n_peers ? String(p.n_peers) : '-'
}

// peerRPCCell builds the Peers-table RPC cell: a plain "-" when the
// peer's RPC was unreachable (no error, just nothing available), or a
// green check with an on-hover summary when one of the probed RPC
// endpoints answered: a link opening its status page, as the
// card's RPC line does, in a new tab, which cannot reach the
// dashboard (the page is the peer's).
/**
 * @param {Peer} p
 * @returns {HTMLTableCellElement}
 */
function peerRPCCell(p) {
    const td = document.createElement('td')
    if (!p.rpc_url) {
        td.textContent = '-'
        td.className = 'full-role'
        return td
    }
    /** @type {PopRow[]} */
    const rows = [
        [null, 'peers', peerCountText(p)],
        [
            null,
            'height',
            (p.height || '?') + (p.catching_up ? ' (syncing)' : ' (synced)'),
        ],
    ]
    const ok = newTabLink(p.rpc_url + '/status', 'ok info-btn')
    ok.textContent = '✓'
    popRows(ok, p.rpc_url, rows)
    td.appendChild(ok)
    return td
}

// renderNodeLists draws the Validators and Peers tables and cards
// when what they show changed since they were last drawn.
export function renderNodeLists() {
    if (!live.dataSeen) return
    const fp = nodeListsFingerprint()
    if (fp === lastNodeListsFingerprint) return
    lastNodeListsFingerprint = fp

    const scrollY = window.scrollY
    const valBody = /** @type {HTMLTableSectionElement} */ (
        $('validators-body')
    )
    const peerBody = /** @type {HTMLTableSectionElement} */ ($('peers-body'))
    const focused = focusPlaceIn(valBody) || focusPlaceIn(peerBody)
    // Country cells collapse to the ISO code when the column is narrow
    // (CSS container query); sort by whatever is shown. Measure the
    // current cells before they're cleared below.
    const valCountryShowsCode = countryShowsCode(valBody)
    const peerCountryShowsCode = countryShowsCode(peerBody)
    clearChildren(valBody)
    clearChildren(peerBody)
    let tbody = valBody

    const votes = live.lastVotes
    const peers = live.lastPeers
    const votesMissedSince =
        votes && votes.missed_since ? votes.missed_since : ''
    const power = votingPower(votes && votes.validators)
    const totalVP = power.total
    // shareOf is voting power n as a whole percentage of the
    // total, null while the total is unknown. isSpof tells whether
    // losing validator v would drop the remaining power below the
    // quorum, halting consensus; null while the total is unknown.
    /**
     * @param {number} n
     * @returns {number | null}
     */
    function shareOf(n) {
        if (totalVP === null) return null
        return totalVP > 0 ? Math.round((n * 100) / totalVP) : 0
    }
    /**
     * @param {VoteInfo} v
     * @returns {boolean | null}
     */
    function isSpof(v) {
        if (power.total === null) return null
        return power.total > 0 && power.total - (powerOf(v) || 0) < power.bft
    }
    // spofText writes whether a validator is a single point of
    // failure, spof as isSpof gives it: "-" while unknown.
    /**
     * @param {boolean | null | undefined} spof
     * @returns {string}
     */
    function spofText(spof) {
        return spof === null ? '-' : spof ? 'yes' : 'no'
    }
    // vpText writes voting power n and its share, which it leaves
    // out while the total is unknown; "-" for an unknown power.
    /**
     * @param {number | null} n
     * @returns {string}
     */
    function vpText(n) {
        if (n === null) return '-'
        const share = shareOf(n)
        return formatVP(n) + (share === null ? '' : ' (' + share + '%)')
    }

    // A validator's node among the peers: the first peer
    // reporting its address, else the first peer named like it.
    // That peer, and no other, leaves the Peers list for the
    // validator's row.
    /** @type {Map<string, Peer>} */
    const peerByAddr = new Map()
    /** @type {Map<string, Peer>} */
    const peerByName = new Map()
    for (const p of peers || []) {
        if (p.val_address && !peerByAddr.has(p.val_address))
            peerByAddr.set(p.val_address, p)
        if (p.moniker && !peerByName.has(p.moniker))
            peerByName.set(p.moniker, p)
    }
    /**
     * @param {VoteInfo} v
     * @returns {Peer | null}
     */
    function nodeOf(v) {
        return (
            peerByAddr.get(v.address) ||
            peerByName.get(validatorName(v.name).name) ||
            null
        )
    }
    /** @type {Set<Peer>} */
    const usedPeers = new Set()
    // The validators in the table's order, which the cards follow.
    /** @type {VoteInfo[]} */
    let sortedVals = []

    // Validators (from consensus) — rendered into the Validators table
    if (votes && votes.validators && votes.validators.length > 0) {
        // Sort validators
        sortedVals = votes.validators.slice()
        // Names, providers and countries shown by name sort as
        // names.
        const valByName =
            valSortKey === 'name' ||
            valSortKey === 'provider' ||
            (valSortKey === 'country' && !valCountryShowsCode)
        sortedVals.sort(function (a, b) {
            let va, vb
            if (valSortKey === 'missed') {
                va = typeof a.missed_100 === 'number' ? a.missed_100 : -1
                vb = typeof b.missed_100 === 'number' ? b.missed_100 : -1
            } else if (valSortKey === 'delay') {
                va =
                    typeof a.vote_delay_ms === 'number' ? a.vote_delay_ms : null
                vb =
                    typeof b.vote_delay_ms === 'number' ? b.vote_delay_ms : null
            } else if (valSortKey === 'props') {
                va = a.expected_proposals_24h
                    ? a.missed_proposals_24h / a.expected_proposals_24h
                    : null
                vb = b.expected_proposals_24h
                    ? b.missed_proposals_24h / b.expected_proposals_24h
                    : null
            } else if (valSortKey === 'missed24h') {
                va = typeof a.missed_24h === 'number' ? a.missed_24h : -1
                vb = typeof b.missed_24h === 'number' ? b.missed_24h : -1
            } else if (valSortKey === 'vp') {
                va = powerOf(a)
                vb = powerOf(b)
            } else if (valSortKey === 'spof') {
                const sa = isSpof(a)
                const sb = isSpof(b)
                va = sa === null ? null : sa ? 1 : 0
                vb = sb === null ? null : sb ? 1 : 0
            } else if (valSortKey === 'address') {
                va = a.address || ''
                vb = b.address || ''
            } else if (valSortKey === 'country' || valSortKey === 'provider') {
                const mca = nodeOf(a)
                const mcb = nodeOf(b)
                va = (mca && mca[valSortKey]) || null
                vb = (mcb && mcb[valSortKey]) || null
                // Match the display: sort by full name unless the
                // column is collapsed to the ISO code.
                if (valSortKey === 'country' && !valCountryShowsCode) {
                    va = va && countryName(va)
                    vb = vb && countryName(vb)
                }
            } else {
                va = validatorName(a.name).name
                vb = validatorName(b.name).name
            }
            return compareSortValues(va, vb, valByName, valSortAsc)
        })

        for (const v of sortedVals) {
            const { name: cleanName, isUs } = validatorName(v.name)
            const mp = nodeOf(v)
            if (mp) usedPeers.add(mp)

            const tr = document.createElement('tr')
            tr.dataset.key = v.address

            // Name
            const nameTd = document.createElement('td')
            nameTd.setAttribute('data-highlight', cleanName)
            nameTd.setAttribute('data-copy', cleanName)
            const displayName = shortenAddressName(cleanName, v.address)
            nameTd.appendChild(valLink(v.address, displayName))
            if (displayName !== cleanName) {
                nameTd.setAttribute('data-popover', cleanName)
                nameTd.classList.add('tap-popover')
            }
            if (isUs)
                nameTd.appendChild(mkEl('span', ' (source)', 'source-tag'))
            tr.appendChild(nameTd)

            // Address (copy includes pubkey when available)
            const addrCopy = v.pub_key ? v.address + ' ' + v.pub_key : v.address
            /** @type {PopRow[]} */
            const addrRows = [[null, 'address', v.address]]
            if (v.pub_key) addrRows.push([null, 'pubkey', v.pub_key])
            tr.appendChild(
                midTruncCell(v.address, {
                    rows: addrRows,
                    copy: addrCopy,
                    highlight: cleanName,
                })
            )

            // Country + provider (from the validator's connected peer)
            tr.appendChild(countryCell(mp ? mp.country || '' : ''))
            tr.appendChild(
                providerCell(
                    mp ? mp.provider || '' : '',
                    mp ? mp.remote_ip || mp.external_address || '' : ''
                )
            )

            // Voting Power
            const vpTd = document.createElement('td')
            vpTd.style.fontSize = '12px'
            const vpNum = powerOf(v)
            const share = vpNum === null ? null : shareOf(vpNum)
            if (vpNum !== null) {
                vpTd.textContent = formatVP(vpNum)
                if (share !== null) {
                    const pctSpan = mkEl('span', '(' + share + '%)', null)
                    pctSpan.style.cssText =
                        'color:var(--fg2);font-weight:normal'
                    vpTd.append(' ', pctSpan)
                }
                // powerOf read a number from it.
                vpTd.setAttribute(
                    'data-copy',
                    /** @type {string} */ (v.voting_power)
                )
            } else {
                vpTd.textContent = '-'
            }
            tr.appendChild(vpTd)

            // SPOF: losing this validator would drop the remaining
            // power below the 2/3+1 quorum, halting consensus.
            const spofTd = document.createElement('td')
            const spof = isSpof(v)
            spofTd.textContent = spofText(spof)
            if (spof) spofTd.className = 'err'
            else spofTd.style.color = 'var(--fg2)'
            tr.appendChild(spofTd)

            // Missed (last 100) — red when the validator is currently
            // flagged down (missed the streak), yellow for stray misses.
            const missed100Td = document.createElement('td')
            const missed100 = v.missed_100
            if (typeof missed100 === 'number') {
                missed100Td.textContent = String(missed100)
                missed100Td.className = v.inactive
                    ? 'err'
                    : missed100 > 0
                      ? 'warn'
                      : 'ok'
                popRows(missed100Td, 'last 100 blocks', missedSplitRows(v))
            } else {
                missed100Td.textContent = '-'
            }
            tr.appendChild(missed100Td)

            // Missed 24h (from the block history)
            const missedTd = document.createElement('td')
            const missed24h = v.missed_24h
            if (typeof missed24h === 'number') {
                missedTd.textContent = String(missed24h)
                missedTd.setAttribute('data-copy', String(missed24h))
                missedTd.className =
                    missed24h === 0 ? 'ok' : missed24h <= 5 ? 'warn' : 'err'
            } else {
                missedTd.textContent = '-'
            }
            if (votesMissedSince) {
                missedTd.setAttribute(
                    'data-popover',
                    'missed in last 24h · history since ' + votesMissedSince
                )
                missedTd.classList.add('tap-popover')
            }
            tr.appendChild(missedTd)

            // Vote delay over the live window
            const delayTd = document.createElement('td')
            delayTd.appendChild(voteDelayNode(v.vote_delay_ms))
            tr.appendChild(delayTd)

            // Missed proposals over the last 24h
            const propsTd = document.createElement('td')
            propsTd.appendChild(
                proposalsNode(v.missed_proposals_24h, v.expected_proposals_24h)
            )
            tr.appendChild(propsTd)

            tbody.appendChild(tr)
        }
    }

    // Source nodes (our own --rpc endpoints) arrive in the peer union
    // flagged source; they render like any peer with a "(source)" tag.
    tbody = peerBody

    // Update both headers with counts
    const valCount = votes && votes.validators ? votes.validators.length : 0
    /** @type {Peer[]} */
    const otherPeers = []
    if (peers) {
        for (const p of peers) {
            if (usedPeers.has(p)) continue
            if (live.hideSources && p.source) continue
            otherPeers.push(p)
        }
    }
    // Names, providers and countries shown by name sort as names.
    const peersByName =
        peerSortKey === 'name' ||
        peerSortKey === 'provider' ||
        (peerSortKey === 'country' && !peerCountryShowsCode)
    // No name, node id, country, provider or peer count (the
    // server leaves a count of 0 out) is null and sorts last
    // either way; the RPC column sorts answered (1) after
    // unanswered (0).
    otherPeers.sort(function (a, b) {
        let va, vb
        if (peerSortKey === 'peers') {
            va = a.n_peers || null
            vb = b.n_peers || null
        } else if (peerSortKey === 'rpc') {
            va = a.rpc_url ? 1 : 0
            vb = b.rpc_url ? 1 : 0
        } else if (peerSortKey === 'nodeid') {
            va = a.node_id || null
            vb = b.node_id || null
        } else if (peerSortKey === 'country' || peerSortKey === 'provider') {
            va = a[peerSortKey] || null
            vb = b[peerSortKey] || null
            // Match the display: sort by full name unless the column
            // is collapsed to the ISO code.
            if (peerSortKey === 'country' && !peerCountryShowsCode) {
                va = va && countryName(va)
                vb = vb && countryName(vb)
            }
        } else {
            va = a.moniker || null
            vb = b.moniker || null
        }
        return compareSortValues(va, vb, peersByName, peerSortAsc)
    })
    // Each title's first child is its button: its label takes the
    // count.
    const valTitle = /** @type {Element} */ (
        $('validators-header').firstElementChild
    )
    valTitle.textContent = 'Validators (' + valCount + ')'
    const peerTitle = /** @type {Element} */ (
        $('peers-header').firstElementChild
    )
    peerTitle.textContent = 'Peers (' + otherPeers.length + ')'

    // Non-validator peers (sentries, full nodes)
    // peerKeys returns a function giving each peer, in list
    // order, its key for the focus keeper: the node id, else the
    // moniker, else the IP and the peer's rank among the peers
    // keyed by it, else its rank among the peers with none; the
    // prefix keeps the kinds apart. The table and the cards take
    // one each, so a peer's row and card share their key.
    /** @returns {(p: Peer) => string} */
    function peerKeys() {
        /** @type {Map<string, number>} */
        const ranks = new Map()
        /**
         * @param {string} ip
         * @returns {number}
         */
        function rank(ip) {
            const n = ranks.get(ip) || 0
            ranks.set(ip, n + 1)
            return n
        }
        return function (p) {
            const ip = p.external_address || p.remote_ip || ''
            return p.node_id
                ? 'id ' + p.node_id
                : p.moniker
                  ? 'name ' + p.moniker
                  : ip
                    ? 'ip ' + ip + ' ' + rank(ip)
                    : 'nameless ' + rank('')
        }
    }
    const rowKey = peerKeys()
    if (otherPeers.length > 0) {
        for (const p of otherPeers) {
            let hlKey = p.moniker || ''
            if (p.val_address) {
                const vn = valAddrToName(p.val_address)
                if (vn) hlKey = vn
            }

            const pIP = p.external_address || p.remote_ip || ''
            const pIPPop = p.remote_ip || pIP
            const pNodeID = p.node_id || ''
            /** @type {PopRow[]} */
            const pNodeIDRows = []
            if (p.val_address)
                pNodeIDRows.push([null, 'address', p.val_address])
            if (p.val_pubkey) pNodeIDRows.push([null, 'pubkey', p.val_pubkey])

            const tr2 = document.createElement('tr')
            tr2.dataset.key = rowKey(p)
            const pNameCell = createCell({
                text: p.moniker || '',
                highlight: hlKey,
                copy: p.moniker || '',
            })
            if (p.source)
                pNameCell.appendChild(mkEl('span', ' (source)', 'source-tag'))
            tr2.appendChild(pNameCell)
            tr2.appendChild(
                pNodeID
                    ? midTruncCell(pNodeID, {
                          rows: pNodeIDRows.length ? pNodeIDRows : undefined,
                          copy: pNodeID,
                          highlight: hlKey,
                      })
                    : createCell({
                          text: '-',
                          style: 'font-size:11px;color:var(--fg2)',
                          highlight: hlKey,
                      })
            )
            tr2.appendChild(countryCell(p.country || '')) // Country
            tr2.appendChild(providerCell(p.provider || '', pIPPop)) // Provider
            tr2.appendChild(
                createCell({
                    text: peerCountText(p),
                    className: 'full-role',
                })
            ) // Peers
            tr2.appendChild(peerRPCCell(p))
            tbody.appendChild(tr2)
        }
    }

    if (!valBody.rows.length) valBody.appendChild(emptyRow('no validators', 10))
    if (!peerBody.rows.length) peerBody.appendChild(emptyRow('no peers', 6))

    window.scrollTo(0, scrollY)
    focusPlace(focused)

    // ---- Validator and peer cards (mobile/tablet)
    function buildPeersCards() {
        const valContainer = $('validators-cards')
        const peerContainer = $('peers-cards')
        // The focus in a card goes back to the same card's
        // control once the lists are drawn again.
        const focusedCard =
            focusPlaceIn(valContainer) || focusPlaceIn(peerContainer)
        clearChildren(valContainer)
        clearChildren(peerContainer)
        const cardKey = peerKeys()

        /**
         * A card's data: its key, for the focus keeper; its name, a
         * validator's linking its page (valAddress); a peer's minimal
         * fields, or a validator's; the RPC footer's; and what a click
         * copies.
         * @typedef {object} CardData
         * @property {string} key
         * @property {string} name
         * @property {string} [valAddress]
         * @property {boolean} isUs
         * @property {boolean | undefined} [source]
         * @property {boolean} [minimal]
         * @property {string} [dotClass]
         * @property {string} [healthText]
         * @property {string} [healthCls]
         * @property {string} [nodeId]
         * @property {string} [ip]
         * @property {string} [address]
         * @property {string} [country]
         * @property {string} [provider]
         * @property {string} [peers]
         * @property {string} [vp]
         * @property {boolean | null} [spof]
         * @property {string} [missed100]
         * @property {string} [missed100Cls]
         * @property {PopRow[] | null} [missedSplit]
         * @property {Node} [delay]
         * @property {string} [missed24h]
         * @property {string} [missedCls]
         * @property {Node} [props]
         * @property {string} [rpcUrl]
         * @property {string} [rpcText]
         * @property {string} [rpcCls]
         * @property {string} [fullAddress]
         * @property {string} [pubKey]
         */
        /**
         * A card's value: its text, or its node; its class; whether it
         * takes a row of its own; the rows of its popover.
         * @typedef {object} CardValue
         * @property {string | undefined} [text]
         * @property {Node | undefined} [node]
         * @property {string | undefined} [cls]
         * @property {boolean} [row]
         * @property {PopRow[] | null | undefined} [split]
         */
        /**
         * @param {CardData} data
         * @returns {HTMLDivElement}
         */
        function makeCard(data) {
            const card = document.createElement('div')
            card.className = 'peer-card ' + (data.rpcUrl ? 'has-rpc' : 'no-rpc')
            card.dataset.key = data.key

            // Top row
            const top = document.createElement('div')
            top.className = 'peer-card-top'
            const nameSpan = document.createElement('span')
            nameSpan.className = 'peer-card-name'
            if (data.dotClass) {
                const dot = mkEl('span', '', 'dot ' + data.dotClass)
                dot.style.cssText =
                    'display:inline-block;width:7px;height:7px;border-radius:50%;margin-right:4px'
                dot.style.background =
                    data.dotClass === 'green'
                        ? 'var(--green)'
                        : data.dotClass === 'red'
                          ? 'var(--red)'
                          : 'var(--yellow)'
                nameSpan.appendChild(dot)
            }
            nameSpan.appendChild(
                data.valAddress
                    ? valLink(data.valAddress, data.name)
                    : document.createTextNode(data.name)
            )
            if (data.isUs || data.source)
                nameSpan.appendChild(mkEl('span', ' (source)', 'source-tag'))
            top.appendChild(nameSpan)
            if (data.healthText) {
                const healthSpan = mkEl('span', data.healthText, data.healthCls)
                healthSpan.style.fontSize = '11px'
                top.appendChild(healthSpan)
            }
            card.appendChild(top)

            // KV body
            const kv = document.createElement('div')
            kv.className = 'peer-card-kv'
            /** @type {Array<[string, string | CardValue]>} */
            const fields = data.minimal
                ? [
                      ['node id', data.nodeId ? shortAddr(data.nodeId) : '-'],
                      ['country', data.country || '-'],
                      ['provider', data.provider || '-'],
                      ['peers', data.peers || '-'],
                  ]
                : // A validator's: signing on its own row, then
                  // pairs, the missed counts on the left.
                  [
                      ['signing', { text: data.address, row: true }],
                      ['country', data.country || '-'],
                      ['provider', data.provider || '-'],
                      ['vp', data.vp || '-'],
                      [
                          'spof',
                          {
                              text: spofText(data.spof),
                              cls: data.spof ? 'err' : '',
                          },
                      ],
                      [
                          'miss 100',
                          {
                              text: data.missed100,
                              cls: data.missed100Cls,
                              split: data.missedSplit,
                          },
                      ],
                      ['delay', { node: data.delay }],
                      [
                          'miss 24h',
                          {
                              text: data.missed24h,
                              cls: data.missedCls,
                          },
                      ],
                      ['props 24h', { node: data.props }],
                  ]
            for (const field of fields) {
                const fk = document.createElement('span')
                fk.className = 'peer-card-k'
                fk.textContent = field[0]
                kv.appendChild(fk)
                const fv = document.createElement('span')
                fv.className = 'peer-card-v'
                const fval = field[1]
                if (typeof fval === 'object' && fval !== null) {
                    if (fval.node) fv.appendChild(fval.node)
                    else fv.textContent = fval.text || '-'
                    if (fval.cls) fv.className += ' ' + fval.cls
                    else if (!fval.node) fv.style.color = 'var(--fg2)'
                    if (fval.row) fv.style.gridColumn = '2 / -1'
                    if (fval.split) popRows(fv, 'last 100 blocks', fval.split)
                } else {
                    fv.textContent = fval || '-'
                    fv.style.color = 'var(--fg2)'
                }
                kv.appendChild(fv)
            }
            card.appendChild(kv)

            // Footer (RPC): when the peer's RPC answered, a link
            // to its status page, opening in a tab that cannot
            // reach the dashboard (the page is the peer's).
            if (data.rpcText) {
                const footer = data.rpcUrl
                    ? newTabLink(data.rpcUrl + '/status', 'peer-card-footer')
                    : mkEl('div', null, 'peer-card-footer')
                const rpcLabel = mkEl('span', 'rpc', 'peer-card-rpc-label')
                const rpcVal = mkEl(
                    'span',
                    data.rpcText,
                    'peer-card-rpc-val ' + (data.rpcCls || 'fg2')
                )
                const hint = mkEl('span', '\u2197', 'peer-card-hint')
                footer.appendChild(rpcLabel)
                footer.appendChild(rpcVal)
                footer.appendChild(hint)
                card.appendChild(footer)
            }

            // Click handler — copy all node info, a line each,
            // none for a name the card lacks.
            const copyLines = data.name ? [data.name] : []
            if (data.fullAddress) copyLines.push('address: ' + data.fullAddress)
            if (data.pubKey) copyLines.push('pubkey: ' + data.pubKey)
            if (data.ip && data.ip !== '-') copyLines.push('ip: ' + data.ip)
            if (data.nodeId && data.nodeId !== '-')
                copyLines.push('node id: ' + data.nodeId)
            if (data.vp && data.vp !== '-') copyLines.push('vp: ' + data.vp)
            if (data.rpcUrl) copyLines.push('rpc: ' + data.rpcUrl)
            const copyText = copyLines.join('\n')
            card.addEventListener('click', function (e) {
                if (!(e.target instanceof Element)) return
                // The footer, a value with a popover and the
                // name's link handle their own taps.
                if (e.target.closest('.peer-card-footer, [data-pop-rows], a'))
                    return
                copyNotify(copyText, e)
            })
            return card
        }

        // Validators
        if (votes && votes.validators && votes.validators.length > 0) {
            for (const v of sortedVals) {
                const { name: cleanName, isUs } = validatorName(v.name)
                const mp = nodeOf(v)
                const vpNum = powerOf(v)

                const missed100 =
                    typeof v.missed_100 === 'number' ? v.missed_100 : null

                valContainer.appendChild(
                    makeCard({
                        key: v.address,
                        name: shortenAddressName(
                            cleanName || v.address,
                            v.address
                        ),
                        valAddress: v.address,
                        isUs: isUs,
                        address: shortAddr(v.address),
                        fullAddress: v.address,
                        pubKey: v.pub_key || '',
                        country:
                            mp && mp.country
                                ? countryFlag(mp.country) +
                                  ' ' +
                                  countryName(mp.country)
                                : '-',
                        provider: mp ? mp.provider || '-' : '-',
                        missed100: missed100 !== null ? String(missed100) : '-',
                        missed100Cls:
                            missed100 !== null
                                ? v.inactive
                                    ? 'err'
                                    : missed100 > 0
                                      ? 'warn'
                                      : 'ok'
                                : '',
                        missed24h:
                            typeof v.missed_24h === 'number'
                                ? String(v.missed_24h)
                                : '-',
                        missedCls:
                            typeof v.missed_24h === 'number'
                                ? v.missed_24h === 0
                                    ? 'ok'
                                    : v.missed_24h <= 5
                                      ? 'warn'
                                      : 'err'
                                : '',
                        missedSplit:
                            missed100 !== null ? missedSplitRows(v) : null,
                        delay: voteDelayNode(v.vote_delay_ms),
                        props: proposalsNode(
                            v.missed_proposals_24h,
                            v.expected_proposals_24h
                        ),
                        vp: vpText(vpNum),
                        spof: isSpof(v),
                    })
                )
            }
        } else {
            valContainer.appendChild(
                mkEl('div', 'no validators', 'full-role cards-empty')
            )
        }

        // Peers (full nodes, sentries)
        if (otherPeers.length > 0) {
            for (const p of otherPeers) {
                const reachable = !!p.rpc_url
                peerContainer.appendChild(
                    makeCard({
                        key: cardKey(p),
                        source: p.source,
                        name: p.moniker || shortAddr(p.val_address || ''),
                        isUs: false,
                        minimal: true,
                        dotClass: reachable ? 'green' : 'yellow',
                        healthText: reachable ? 'reachable' : 'unreachable',
                        healthCls: reachable ? 'ok' : '',
                        nodeId: p.node_id || '',
                        ip: p.external_address || p.remote_ip || '-',
                        country: p.country
                            ? countryFlag(p.country) +
                              ' ' +
                              countryName(p.country)
                            : '-',
                        provider: p.provider || '-',
                        fullAddress: p.val_address || '',
                        pubKey: p.val_pubkey || '',
                        peers: peerCountText(p),
                        rpcUrl: p.rpc_url || '',
                        rpcText: reachable ? '\u2713 ' + p.rpc_url : '\u2014',
                        rpcCls: reachable ? 'ok' : 'fg2',
                    })
                )
            }
        } else {
            peerContainer.appendChild(
                mkEl('div', 'no peers', 'full-role cards-empty')
            )
        }
        const valCompact = /** @type {HTMLInputElement} */ (
            $('validators-compact')
        )
        applyCompact('validators-card', 'validators-cards', valCompact.checked)
        const peersCompact = /** @type {HTMLInputElement} */ (
            $('peers-compact')
        )
        applyCompact('peers-card', 'peers-cards', peersCompact.checked)
        focusPlace(focusedCard)
    }
    buildPeersCards()
    // The redrawn cells the pointer is on take their highlights.
    const at = pointedElement()
    refreshHighlight(at)
    refreshCountryHighlight(at)
}

// Mark the active sort column on each table: blue label + a ▲/▼ arrow
// for ascending/descending. It runs as the order changes.
function updateSortHeaders() {
    const ths = document.querySelectorAll('th[data-sort]')
    for (const th of ths) {
        const table = th.closest('table')
        const isPeer = !!(table && table.querySelector('#peers-body'))
        const key = isPeer ? peerSortKey : valSortKey
        const asc = isPeer ? peerSortAsc : valSortAsc
        const active = th.getAttribute('data-sort') === key
        th.classList.toggle('sorted-col', active)
        if (active)
            th.setAttribute('aria-sort', asc ? 'ascending' : 'descending')
        else th.removeAttribute('aria-sort')
        let arrow = th.querySelector('.sort-arrow')
        if (active) {
            if (!arrow) {
                arrow = document.createElement('span')
                arrow.className = 'sort-arrow'
                // aria-sort tells the order: the arrow is no
                // part of the header's name.
                arrow.setAttribute('aria-hidden', 'true')
                $in(th, 'button').appendChild(arrow)
            }
            // A no-break space joins the arrow to the label's last
            // word: a wrapping header never leaves it alone.
            arrow.textContent = asc ? '\u00a0▲' : '\u00a0▼'
        } else if (arrow) {
            arrow.remove()
        }
    }
}

// Compact toggle (mobile/tablet): swap a node-list's table for its card view.
/**
 * @param {string} cardId
 * @param {string} cardsId
 * @param {boolean} compact
 */
function applyCompact(cardId, cardsId, compact) {
    const cards = $(cardsId)
    const wrap = $in(document, '#' + cardId + ' .table-wrap')
    // Only takes effect under the ≤768px media query; at wider widths
    // CSS always shows the table.
    if (compact) {
        cards.style.display = 'none'
        wrap.style.display = 'block'
    } else {
        cards.style.display = ''
        wrap.style.display = ''
    }
}

// setupCompact keeps a node-list's view as its checkbox cbId says,
// stored at storeKey.
/**
 * @param {string} cbId
 * @param {string} cardId
 * @param {string} cardsId
 * @param {string} storeKey
 */
function setupCompact(cbId, cardId, cardsId, storeKey) {
    const cb = /** @type {HTMLInputElement} */ ($(cbId))
    const on = storageGet(storeKey) === 'true'
    cb.checked = on
    applyCompact(cardId, cardsId, on)
    cb.addEventListener('change', function () {
        storageSet(storeKey, cb.checked ? 'true' : 'false')
        applyCompact(cardId, cardsId, cb.checked)
    })
}

export function start() {
    // A click on a header, its button's from the keyboard included,
    // sorts by its column.
    document.addEventListener('click', function (e) {
        if (!(e.target instanceof Element)) return
        const th = e.target.closest('th[data-sort]')
        if (th) sortBy(th)
    })
    // A header's label ends where its sort arrow joins it.
    for (const b of document.querySelectorAll('th[data-sort] > button'))
        b.textContent = b.textContent.trim()
    // The headers show the order from the start, before any data.
    updateSortHeaders()
    setupCompact(
        'validators-compact',
        'validators-card',
        'validators-cards',
        'validators-compact'
    )
    setupCompact('peers-compact', 'peers-card', 'peers-cards', 'peers-compact')
}
