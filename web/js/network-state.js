// The Network State card: the consensus round, its votes and the voting
// power behind them, and the card's compact view.

/** @import { VotesReport } from '../types/node.js' */
/** @import { VotingPower } from './format.js' */

import {
    $,
    $in,
    clearChildren,
    mkEl,
    setText,
    setTextWithClass,
} from './dom.js'
import { ageText, formatVP, votingPower } from './format.js'
import { shortenAddressName, validatorName } from './names.js'
import { renderNodeLists } from './node-lists.js'
import { live } from './state.js'
import { storageGet, storageSet } from './storage.js'

// scheduleRenderNetwork draws the data at the next animation
// frame: any number of calls before it make one drawing.
let _renderNetworkPending = false
export function scheduleRenderNetwork() {
    if (!_renderNetworkPending) {
        _renderNetworkPending = true
        requestAnimationFrame(function () {
            _renderNetworkPending = false
            renderNetwork()
        })
    }
}

// renderNetwork draws the Network State's consensus values, and
// the tables and cards when what they show changed.
function renderNetwork() {
    renderConsensus()
    renderNodeLists()
}

// renderConsensus shows the votes in the Network State card: the
// height, round and step, the proposer, the voting power that
// voted and the active voting power; "-" without votes.
function renderConsensus() {
    const votes = live.lastVotes
    renderProposer(votes)
    if (!votes) {
        // The sources are down.
        setText('ns-consensus', '-')
        renderVotes(null)
        renderActiveVP(null)
        return
    }
    setText(
        'ns-consensus',
        'h=' + votes.height + ' r=' + votes.round + ' s=' + votes.step
    )
    const vp = votingPower(votes.validators)
    renderVotes(vp)
    renderActiveVP(vp)
}

// renderVotes shows the voting power that prevoted and that
// precommitted, of the total, each coloured by the BFT quorum (⅔
// of the total, plus one): ok from it, warn from 80 % of it; "-"
// without votes (vp null), while the total is unknown (any one
// validator's voting power is), or when it is 0.
/** @param {VotingPower | { total: null } | null} vp */
function renderVotes(vp) {
    if (!vp || vp.total === null || !(vp.total > 0)) {
        setText('ns-votes', '-')
        return
    }
    /** @param {number} n */
    const band = function (n) {
        return n >= vp.bft ? 'ok' : n >= vp.bft * 0.8 ? 'warn' : 'err'
    }
    const votesEl = $('ns-votes')
    votesEl.replaceChildren(
        mkEl('span', formatVP(vp.prevoted), band(vp.prevoted)),
        '/',
        mkEl('span', formatVP(vp.precommitted), band(vp.precommitted)),
        ' of ' + formatVP(vp.total)
    )
    votesEl.className = 'v'
}

// renderActiveVP shows the active validators' voting power of the
// total, with its margin over the BFT quorum, coloured by it; "-"
// without votes (vp null), while the total is unknown (any one
// validator's voting power is), or when it is 0.
/** @param {VotingPower | { total: null } | null} vp */
function renderActiveVP(vp) {
    const activeVpEl = $('ns-active-vp')
    if (!vp || vp.total === null || !(vp.total > 0)) {
        activeVpEl.textContent = '-'
        activeVpEl.className = 'v'
        return
    }
    const vpMargin = vp.active - vp.bft
    activeVpEl.textContent =
        formatVP(vp.active) +
        '/' +
        formatVP(vp.total) +
        ' (margin: ' +
        (vpMargin >= 0 ? '+' : '') +
        formatVP(vpMargin) +
        ')'
    activeVpEl.className =
        vpMargin >= vp.total * 0.1 ? 'v ok' : vpMargin > 0 ? 'v warn' : 'v err'
}

// renderProposer shows the votes' proposer as the tables show a
// validator: its name or, when it is named by its address, the
// address shortened, the full one in a popover; tagged (source)
// when it is ours; pointing at it highlights its row, a click
// copies its full name. "-" without votes or a proposer. An
// unchanged proposer keeps its element, and an open popover on it.
/** @type {string | null} */
let shownProposer = null
/** @param {VotesReport | null} votes */
function renderProposer(votes) {
    const name = (votes && votes.proposer) || null
    if (name === shownProposer) return
    shownProposer = name
    const propEl = $('ns-proposer')
    clearChildren(propEl)
    // No name without votes.
    if (!votes || name === null) {
        propEl.textContent = '-'
        propEl.removeAttribute('data-copy')
        return
    }
    const { name: cleanName, isUs } = validatorName(name)
    const proposer = (votes.validators || []).find((v) => v.name === name)
    const propDisplay = shortenAddressName(
        cleanName,
        proposer ? proposer.address : ''
    )
    const propSpan = mkEl('span', propDisplay, 'node-ref')
    propSpan.setAttribute('data-highlight', cleanName)
    if (propDisplay !== cleanName) {
        propSpan.setAttribute('data-popover', cleanName)
        propSpan.classList.add('tap-popover')
    }
    propEl.appendChild(propSpan)
    if (isUs) propEl.appendChild(mkEl('span', ' (source)', 'source-tag'))
    propEl.setAttribute('data-copy', cleanName)
    propEl.className = 'v'
}

// renderRoundAge shows how long the round has run as of now (ms
// since the epoch): green, warn past 10 s, err past 30 s; "-"
// without a valid start. It flashes for a new round, as a value
// does when its data changes, not as the seconds go by.
/** @type {string | undefined | null} */
let shownRoundStart = null
/** @param {number} now */
export function renderRoundAge(now) {
    const start = live.lastVotes ? live.lastVotes.round_start_time : null
    const flash = start !== shownRoundStart
    shownRoundStart = start
    const ageSec = roundAgeSec(now)
    if (Number.isNaN(ageSec)) {
        setTextWithClass('ns-roundage', '-', 'v', flash)
        return
    }
    setTextWithClass(
        'ns-roundage',
        ageText(ageSec),
        ageSec > 30 ? 'v err' : ageSec > 10 ? 'v warn' : 'v ok',
        flash
    )
}

// roundAgeSec is how many whole seconds the round has run as of
// now, 0 for a start later than now; NaN without votes or a valid
// start. The round age and the stuck tag both read it.
/**
 * @param {number} now
 * @returns {number}
 */
export function roundAgeSec(now) {
    const start = live.lastVotes ? live.lastVotes.round_start_time : undefined
    const rst = start === undefined ? NaN : Date.parse(start)
    return Math.max(0, Math.floor((now - rst) / 1000))
}

export function start() {
    // The section's title sits in its card.
    const nsCard = /** @type {Element} */ (
        $in(document, '[data-section="network-state"]').closest('.card')
    )
    const cb = /** @type {HTMLInputElement} */ ($('ns-compact-cb'))
    // isMobile is a page-load snapshot — intentionally not reactive to resize.
    // It asks the stylesheet's own query, which a fractional width (browser
    // zoom) meets or not as the stylesheet does; innerWidth counts whole
    // pixels.
    const isMobile = window.matchMedia('(max-width: 768px)').matches
    // Default compact ON on mobile: null (first visit) !== '0' → true
    const on = isMobile ? storageGet('ns-compact') !== '0' : false
    // compact-off overrides the stylesheet's compact default up to 768 px,
    // so the mode follows this load's choice whatever the width becomes.
    nsCard.classList.toggle('compact-on', on)
    nsCard.classList.toggle('compact-off', !on)
    cb.checked = on
    cb.addEventListener('change', function () {
        nsCard.classList.toggle('compact-on', this.checked)
        nsCard.classList.toggle('compact-off', !this.checked)
        storageSet('ns-compact', this.checked ? '1' : '0')
    })
}
