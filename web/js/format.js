// How the page writes values: numbers, hashes, dates, times and ages,
// scores, vote delays and voting powers.

/** @import { VoteInfo } from '../types/node.js' */

import { live } from './state.js'

// b64toHex writes base64 b64 in upper-case hex; b64 as it is when it
// is no base64.
/**
 * @param {string} b64
 * @returns {string}
 */
export function b64toHex(b64) {
    try {
        const raw = atob(b64)
        let hex = ''
        for (let i = 0; i < raw.length; i++) {
            const h = raw.charCodeAt(i).toString(16)
            hex += h.length < 2 ? '0' + h : h
        }
        return hex.toUpperCase()
    } catch (e) {
        return b64
    }
}

// timeAgo writes how long ago an RFC 3339 time was, roughly, as of
// now (ms since the epoch); by default as of the server's clock,
// since the browser's may be off.
/**
 * @param {string} iso
 * @param {number} [now]
 * @returns {string}
 */
export function timeAgo(iso, now) {
    const at = now === undefined ? Date.now() + live.serverTimeOffset : now
    const m = Math.round((at - Date.parse(iso)) / 60000)
    if (m < 1) return 'just now'
    if (m < 60) return m + ' min ago'
    const h = Math.round(m / 60)
    if (h < 48) return h + ' h ago'
    return Math.round(h / 24) + ' d ago'
}

// thousands writes n with its thousands separated, as en-US does:
// 1250000 as 1,250,000.
/**
 * @param {number} n
 * @returns {string}
 */
export function thousands(n) {
    return n.toLocaleString('en-US')
}

// twoDigits writes n with two digits at least: 7 as 07.
/**
 * @param {number} n
 * @returns {string}
 */
export function twoDigits(n) {
    return String(n).padStart(2, '0')
}

// MONTHS are the months' short English names, January first, as a
// Date's getUTCMonth indexes them.
/** @type {readonly string[]} */
export const MONTHS = [
    'Jan',
    'Feb',
    'Mar',
    'Apr',
    'May',
    'Jun',
    'Jul',
    'Aug',
    'Sep',
    'Oct',
    'Nov',
    'Dec',
]

// utcMinute writes an RFC 3339 UTC time to the minute.
/**
 * @param {string} iso
 * @returns {string}
 */
export function utcMinute(iso) {
    return iso.slice(0, 16).replace('T', ' ') + ' UTC'
}

/** @typedef {'ok' | 'warn' | 'err'} Band */
// scoreBand classes a scored value by where it lands on the
// scoring map: ok where the sub-score is full, warn partial, err
// zero.
/** @type {Record<'uptime' | 'outages' | 'proposals' | 'recovery', (value: number) => Band>} */
export const scoreBand = {
    uptime: function (pct) {
        return pct >= 99.9 ? 'ok' : pct > 95 ? 'warn' : 'err'
    },
    outages: function (n) {
        return n === 0 ? 'ok' : n < 10 ? 'warn' : 'err'
    },
    proposals: function (pct) {
        return pct === 0 ? 'ok' : pct < 5 ? 'warn' : 'err'
    },
    recovery: function (ms) {
        return ms <= 600000 ? 'ok' : ms < 86400000 ? 'warn' : 'err'
    },
}

// voteDelayText writes a vote delay in seconds, signed: negative
// is early; one that rounds to zero has no sign.
/**
 * @param {number} ms
 * @returns {string}
 */
export function voteDelayText(ms) {
    const s = (Math.abs(ms) / 1000).toFixed(2)
    if (s === '0.00') return s + ' s'
    return (ms < 0 ? '−' : '+') + s + ' s'
}

// voteDelayClass colours a vote delay by the vote-delay chart's
// ranges: within 1 s ok, to 2.5 s warn, beyond err.
/**
 * @param {number} ms
 * @returns {string}
 */
export function voteDelayClass(ms) {
    const a = Math.abs(ms)
    return a <= 1000 ? 'ok' : a <= 2500 ? 'warn' : 'err'
}

// powerOf is validator v's voting power, null when it has none
// the page can read as a number.
/**
 * @param {VoteInfo} v
 * @returns {number | null}
 */
export function powerOf(v) {
    if (v.voting_power === undefined) return null
    const n = parseInt(v.voting_power, 10)
    return Number.isFinite(n) ? n : null
}

// VP_UNITS are the units formatVP writes a voting power in,
// largest first.
/** @type {Array<[string, number]>} */
const VP_UNITS = [
    ['B', 1e9],
    ['M', 1e6],
    ['K', 1e3],
]

// formatVP writes a voting power whole below a thousand, else to
// a tenth in the largest unit it reaches once rounded: a unit
// starts where the unit below would round to 1000.0, so 999,950
// writes 1.0M.
/**
 * @param {number} n
 * @returns {string}
 */
export function formatVP(n) {
    const abs = Math.abs(n)
    const sign = n < 0 ? '-' : ''
    for (const [unit, size] of VP_UNITS) {
        if (abs >= size - size / 20000)
            return sign + (abs / size).toFixed(1) + unit
    }
    return String(n)
}

// votingPower sums the validators' voting power: all of it, that
// of those that prevoted, precommitted and are active, and the BFT
// quorum (⅔ of all, plus one). While any validator's power is
// unknown (powerOf null) so is the total: it is null, and no share
// or quorum holds.
/**
 * @typedef {object} VotingPower
 * @property {number} total
 * @property {number} prevoted
 * @property {number} precommitted
 * @property {number} active
 * @property {number} bft the BFT quorum
 */
/**
 * @param {VoteInfo[] | null} validators
 * @returns {VotingPower | { total: null }}
 */
export function votingPower(validators) {
    const vals = validators || []
    /** @type {VotingPower} */
    const vp = { total: 0, prevoted: 0, precommitted: 0, active: 0, bft: 0 }
    for (const v of vals) {
        const n = powerOf(v)
        if (n === null) return { total: null }
        vp.total += n
        if (v.prevoted) vp.prevoted += n
        if (v.precommit) vp.precommitted += n
        if (!v.inactive) vp.active += n
    }
    vp.bft = Math.floor((vp.total * 2) / 3) + 1
    return vp
}

// ageText writes sec seconds in their two largest units: 45s,
// 2m 5s, 3h 10m, 2d 4h.
/**
 * @param {number} sec
 * @returns {string}
 */
export function ageText(sec) {
    if (sec < 60) return sec + 's'
    if (sec < 3600) return Math.floor(sec / 60) + 'm ' + (sec % 60) + 's'
    if (sec < 86400)
        return (
            Math.floor(sec / 3600) + 'h ' + Math.floor((sec % 3600) / 60) + 'm'
        )
    return (
        Math.floor(sec / 86400) + 'd ' + Math.floor((sec % 86400) / 3600) + 'h'
    )
}
