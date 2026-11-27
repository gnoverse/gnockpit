// The Validators and Peers cards: each a table sorted by the column the
// viewer picks, and a card view for narrow screens.

/** @import { Peer, Presence, VoteInfo } from '../types/node.js' */
/** @import { DeclaredNode } from '../types/server.js' */
/** @import { PopRow } from './popover.js' */

import { compareSortValues } from './collate.js'
import { copyNotify } from './copy.js'
import { countryFlag, countryName } from './countries.js'
import { refreshCountryHighlight } from './country-highlight.js'
import { $, $in, clearChildren, mkEl, mkLink, newTabLink } from './dom.js'
import { focusPlace, focusPlaceIn } from './focus.js'
import {
    formatVP,
    pctDown,
    powerOf,
    scoreBand,
    utcMinute,
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
/**
 * Where a validator runs, as its row and card show it: its country and
 * provider, '' when unknown, and the IP they were guessed from; or, with its
 * declaration, the declared ones, no IP.
 * @typedef {object} ValidatorLoc
 * @property {string} country
 * @property {string} provider
 * @property {string} ip
 * @property {DeclaredNode | null} declared
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

// countryCell builds a table cell showing a node's country: flag +
// full name, collapsing to the ISO code as the column narrows (CSS
// container query). data-cc cross-links it to the network map. Its
// popover names the country, and says so of one an operator
// declared.
/**
 * @param {string} code
 * @param {DeclaredNode | null} [declared] the declaration the country is
 *   from
 * @returns {HTMLTableCellElement}
 */
function countryCell(code, declared) {
    const td = document.createElement('td')
    td.className = 'country-cell'
    if (!code) {
        td.textContent = '—'
        return td
    }
    td.setAttribute('data-cc', code)
    td.setAttribute('data-copy', countryName(code))
    td.setAttribute(
        'data-popover',
        countryName(code) + (declared ? ' · declared by the operator' : '')
    )
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

// declaredDay is the day, in UTC, of declaration d, as the tables
// show it.
/**
 * @param {DeclaredNode} d
 * @returns {string}
 */
function declaredDay(d) {
    return d.at.slice(0, 10)
}

// providerCell builds a cell showing the cloud provider with an info
// icon whose popover reveals where it is from: the IP it was guessed
// from, or the day its operator declared it.
/**
 * @param {string} provider
 * @param {string} ip
 * @param {DeclaredNode | null} [declared] the declaration the provider is
 *   from
 * @returns {HTMLTableCellElement}
 */
function providerCell(provider, ip, declared) {
    /** @type {PopRow[] | undefined} */
    const rows = declared
        ? [[null, 'declared by the operator', declaredDay(declared)]]
        : ip
          ? [[null, 'guessed from IP', ip]]
          : undefined
    if (!provider && !rows) {
        const empty = document.createElement('td')
        empty.textContent = '—'
        return empty
    }
    const td = createCell({
        text: provider || 'unknown',
        style: 'font-size:11px;color:var(--fg2)',
        rows: rows,
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
                connectedText(p),
                onlineText(p),
                p.presence ? firstSeenText(p.presence) : '',
                p.rpc_url,
                p.height,
                p.catching_up,
                p.source,
            ]
        }),
        // A declaration's key, country, provider and day: its time
        // within the day, its place and its name the tables do not
        // show.
        Object.entries(live.declared).map(function ([key, d]) {
            return [key, d.country, d.provider, declaredDay(d)]
        }),
    ])
}

// connectedShown is how long peer p has been connected as the page
// writes it, in minutes, rounded down: to the minute below a day, to
// the hour from a day up; null when no source reports a connection
// time, which the server leaves out at 0. The Connected column sorts
// by it, so times that read the same tie.
/**
 * @param {Peer} p
 * @returns {number | null}
 */
function connectedShown(p) {
    if (!p.connected_ms) return null
    const minutes = Math.floor(p.connected_ms / 60000)
    return minutes < 24 * 60 ? minutes : Math.floor(minutes / 60) * 60
}

// connectedText writes connectedShown in its two largest units:
// 3 d 4 h, 5 h 12 min, 12 min, < 1 min; "-" without it. A no-break
// space joins each number to its unit, so a wrapping value never
// leaves a unit alone; the space between two units' groups breaks.
/**
 * @param {Peer} p
 * @returns {string}
 */
function connectedText(p) {
    const minutes = connectedShown(p)
    if (minutes === null) return '-'
    if (minutes < 1) return '<\u00a01\u00a0min'
    if (minutes < 60) return minutes + '\u00a0min'
    const hours = Math.floor(minutes / 60)
    if (hours < 24) return hours + '\u00a0h ' + (minutes % 60) + '\u00a0min'
    return Math.floor(hours / 24) + '\u00a0d ' + (hours % 24) + '\u00a0h'
}

// onlineShown is peer p's share of gnockpit's polls over the window as
// the page writes it: a percentage rounded down to a tenth, from the
// exact counts; null without its presence. The Online column sorts by
// it, so shares that read the same tie.
/**
 * @param {Peer} p
 * @returns {number | null}
 */
function onlineShown(p) {
    const pr = p.presence
    return pr ? pctDown(pr.listed, pr.polls, 1) : null
}

// onlineText writes onlineShown joined to its unit by a no-break
// space; "-" without it.
/**
 * @param {Peer} p
 * @returns {string}
 */
function onlineText(p) {
    const pct = onlineShown(p)
    return pct === null ? '-' : pct.toFixed(1) + '\u00a0%'
}

// firstSeenText writes when gnockpit first saw a peer: the UTC minute
// of the first stored poll that listed it, or "≤" and that poll's day
// when it was the chain's first stored poll, the peer perhaps there
// before.
/**
 * @param {Presence} pr
 * @returns {string}
 */
function firstSeenText(pr) {
    return pr.from_start
        ? '≤ ' + pr.first_seen.slice(0, 10)
        : utcMinute(pr.first_seen)
}

// peerOnlineCell builds the Peers-table Online cell: the peer's share
// of the polls, when gnockpit first saw it in a popover; "-" without
// its presence.
/**
 * @param {Peer} p
 * @returns {HTMLTableCellElement}
 */
function peerOnlineCell(p) {
    const td = createCell({
        text: onlineText(p),
        className: 'full-role peer-figure',
    })
    if (p.presence)
        popRows(td, '', [[null, 'first seen', firstSeenText(p.presence)]])
    return td
}

// peerRPCCell builds the Peers-table RPC cell: "closed" when none of
// the peer's probed RPC endpoints answered; "open" when one did, a
// link opening its status page, as the card's RPC line does, in a new
// tab, which cannot reach the dashboard (the page is the peer's), the
// endpoint and its height in a popover.
/**
 * @param {Peer} p
 * @returns {HTMLTableCellElement}
 */
function peerRPCCell(p) {
    if (!p.rpc_url)
        return createCell({
            text: 'closed',
            className: 'full-role peer-figure',
        })
    const open = newTabLink(p.rpc_url + '/status', 'ok info-btn')
    open.textContent = 'open'
    popRows(open, p.rpc_url, [
        [
            null,
            'height',
            (p.height || '?') + (p.catching_up ? ' (syncing)' : ' (synced)'),
        ],
    ])
    const td = mkEl('td', null, 'peer-figure')
    td.appendChild(open)
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
    // Where validator v runs: its declared location, which the
    // server sends only while no IP locates v, else its node's.
    /**
     * @param {VoteInfo} v
     * @returns {ValidatorLoc}
     */
    function locOf(v) {
        const declared =
            (Object.hasOwn(live.declared, v.address) &&
                live.declared[v.address]) ||
            null
        if (declared)
            return {
                country: declared.country,
                provider: declared.provider || '',
                ip: '',
                declared: declared,
            }
        const mp = nodeOf(v)
        return {
            country: (mp && mp.country) || '',
            provider: (mp && mp.provider) || '',
            ip: (mp && (mp.remote_ip || mp.external_address)) || '',
            declared: null,
        }
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
                va = locOf(a)[valSortKey] || null
                vb = locOf(b)[valSortKey] || null
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

            // Country + provider (declared, else from the validator's
            // connected peer)
            const loc = locOf(v)
            tr.appendChild(countryCell(loc.country, loc.declared))
            tr.appendChild(providerCell(loc.provider, loc.ip, loc.declared))

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
    // No name, node id, country, provider, connection time (the
    // server leaves a time of 0 out) or presence is null and sorts
    // last either way; Connected and Online sort by the values they
    // show; the RPC column sorts answered (1) after unanswered (0).
    otherPeers.sort(function (a, b) {
        let va, vb
        if (peerSortKey === 'connected') {
            va = connectedShown(a)
            vb = connectedShown(b)
        } else if (peerSortKey === 'online') {
            va = onlineShown(a)
            vb = onlineShown(b)
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
                    text: connectedText(p),
                    className: 'full-role peer-figure',
                })
            ) // Connected
            tr2.appendChild(peerOnlineCell(p))
            tr2.appendChild(peerRPCCell(p))
            tbody.appendChild(tr2)
        }
    }

    if (!valBody.rows.length) valBody.appendChild(emptyRow('no validators', 10))
    if (!peerBody.rows.length) peerBody.appendChild(emptyRow('no peers', 7))

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
         * fields, or a validator's, its country and provider declared
         * or not; the RPC footer's; and what a click copies.
         * @typedef {object} CardData
         * @property {string} key
         * @property {string} name
         * @property {string} [valAddress]
         * @property {boolean} isUs
         * @property {boolean | undefined} [source]
         * @property {boolean} [minimal]
         * @property {string} [dotClass]
         * @property {string} [nodeId]
         * @property {string} [ip]
         * @property {string} [address]
         * @property {string} [country]
         * @property {string} [provider]
         * @property {boolean} [declared]
         * @property {string} [connected]
         * @property {string} [online]
         * @property {string} [firstSeen]
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
         * takes a row of its own; whether it wraps, rather than clip, in
         * a narrow column; the rows of its popover.
         * @typedef {object} CardValue
         * @property {string | undefined} [text]
         * @property {Node | undefined} [node]
         * @property {string | undefined} [cls]
         * @property {boolean} [row]
         * @property {boolean} [wrap]
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
                    data.dotClass === 'green' ? 'var(--green)' : 'var(--fg2)'
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
            card.appendChild(top)

            // KV body
            const kv = document.createElement('div')
            kv.className = 'peer-card-kv'
            /** @type {Array<[string, string | CardValue]>} */
            const fields = data.minimal
                ? // A peer's: pairs, then first seen, too long for a
                  // pair, on its own row. Connected and online wrap
                  // in the narrow columns of cards two abreast.
                  [
                      ['node id', data.nodeId ? shortAddr(data.nodeId) : '-'],
                      ['country', data.country || '-'],
                      ['provider', data.provider || '-'],
                      ['connected', { text: data.connected, wrap: true }],
                      ['online 30d', { text: data.online, wrap: true }],
                      ['first seen', { text: data.firstSeen, row: true }],
                  ]
                : // A validator's: signing on its own row, then
                  // pairs, the missed counts on the left. A declared
                  // country and provider, longer for their mark, wrap
                  // in the narrow columns.
                  [
                      ['signing', { text: data.address, row: true }],
                      [
                          'country',
                          { text: data.country, wrap: !!data.declared },
                      ],
                      [
                          'provider',
                          { text: data.provider, wrap: !!data.declared },
                      ],
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
                    // A value on a row of its own starts the row, its
                    // label in the first column, and spans the rest.
                    if (fval.row) {
                        fk.style.gridColumn = '1'
                        fv.style.gridColumn = '2 / -1'
                    }
                    if (fval.wrap) fv.style.whiteSpace = 'normal'
                    if (fval.split) popRows(fv, 'last 100 blocks', fval.split)
                } else {
                    fv.textContent = fval || '-'
                    fv.style.color = 'var(--fg2)'
                }
                kv.appendChild(fv)
            }
            card.appendChild(kv)

            // Footer (RPC): when the peer's RPC answered, a link
            // to its status page reading its endpoint, opening in
            // a tab that cannot reach the dashboard (the page is
            // the peer's); "closed" otherwise.
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
                const loc = locOf(v)
                // A declared country and provider are marked so.
                const mark = loc.declared ? ' (declared)' : ''
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
                        declared: loc.declared !== null,
                        country: loc.country
                            ? countryFlag(loc.country) +
                              ' ' +
                              countryName(loc.country) +
                              mark
                            : '-',
                        // A declaration naming no provider leaves it
                        // unknown, as the table writes it.
                        provider: loc.declared
                            ? (loc.provider || 'unknown') + mark
                            : loc.provider || '-',
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
                const rpcOpen = !!p.rpc_url
                peerContainer.appendChild(
                    makeCard({
                        key: cardKey(p),
                        source: p.source,
                        name: p.moniker || shortAddr(p.val_address || ''),
                        isUs: false,
                        minimal: true,
                        dotClass: rpcOpen ? 'green' : 'grey',
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
                        connected: connectedText(p),
                        online: onlineText(p),
                        firstSeen: p.presence ? firstSeenText(p.presence) : '-',
                        rpcUrl: p.rpc_url || '',
                        rpcText: p.rpc_url || 'closed',
                        rpcCls: rpcOpen ? 'ok' : 'fg2',
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
