// How the page writes values: numbers and percentages, hashes, dates,
// times, durations and ages, scores, vote delays and voting powers. A
// figure the ranking scores is never written better than its value:
// one where higher is better (an uptime, a sub-score, points, a score)
// rounded down, one where lower is better (a missed share, a recovery
// time) rounded up.

/** @import { VoteInfo } from '../types/node.js' */
/** @import { Part } from '../types/server.js' */

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

// durationText writes a duration: seconds kept under an hour, days
// from 48 h.
/**
 * @param {number} ms
 * @returns {string}
 */
export function durationText(ms) {
    const s = Math.round(ms / 1000)
    if (s < 60) return s + ' s'
    if (s < 3600) return Math.floor(s / 60) + ' min ' + twoDigits(s % 60) + ' s'
    const h = Math.floor(s / 3600)
    if (h >= 48) return Math.round(h / 24) + ' d'
    return h + ' h ' + twoDigits(Math.floor((s % 3600) / 60)) + ' min'
}

// durationUpText writes a duration rounded up at the last unit
// written: a time where shorter is better, such as a recovery, never
// reads shorter than it was. Seconds under an hour, as durationText
// writes them, so 600.4 s is written 10 min 01 s, past the 10 minutes
// of full marks; minutes to 48 h; days and hours from 48 h, so 48 h
// and 1 ms is written 2 d 01 h.
/**
 * @param {number} ms
 * @returns {string}
 */
export function durationUpText(ms) {
    const s = Math.ceil(ms / 1000)
    if (s < 3600) return durationText(s * 1000)
    const m = Math.ceil(s / 60)
    if (m < 48 * 60)
        return Math.floor(m / 60) + ' h ' + twoDigits(m % 60) + ' min'
    const h = Math.ceil(m / 60)
    return Math.floor(h / 24) + ' d ' + twoDigits(h % 24) + ' h'
}

// pctDown is n of total as a percentage rounded down to digits
// decimals, from the exact counts: a figure reaches a threshold
// only when the counts do, so 99.895 % is written 99.89 %.
/**
 * @param {number} n
 * @param {number} total
 * @param {number} digits
 * @returns {number}
 */
export function pctDown(n, total, digits) {
    const scale = 10 ** digits
    return Math.floor((n * 100 * scale) / total) / scale
}

// pctUp is n of total as a percentage rounded up to digits
// decimals, from the exact counts: a miss never reads none, so 1 of
// 26,000 is written 0.01 %.
/**
 * @param {number} n
 * @param {number} total
 * @param {number} digits
 * @returns {number}
 */
export function pctUp(n, total, digits) {
    const scale = 10 ** digits
    return Math.ceil((n * 100 * scale) / total) / scale
}

// floorNudged rounds x, a scaled share, down to a whole number, a
// nudge far below one block of any chain absorbing the float error
// of the scaling: 0.29 × 100 is 28.999999999999996, rounded to 29.
/**
 * @param {number} x
 * @returns {number}
 */
function floorNudged(x) {
    return Math.floor(x + 1e-9)
}

// sharePctDown is a share of 1 as a whole percentage, rounded down
// (floorNudged): 0.29 is written 29, and a share one block short of
// whole stays under 100.
/**
 * @param {number} share
 * @returns {number}
 */
export function sharePctDown(share) {
    return floorNudged(share * 100)
}

// subScoreText writes a ranking's sub-score, 0 to 1, to 3 decimals,
// rounded down (floorNudged), so it reads 1.000 only at full marks.
/**
 * @param {number} sub
 * @returns {string}
 */
export function subScoreText(sub) {
    return (floorNudged(sub * 1000) / 1000).toFixed(3)
}

// scoreText writes a ranking's score to one decimal, rounded down
// (floorNudged), so 99.96 reads 99.9, never 100.0: the daily alert
// writes a score the same way (ranking/digest.go).
/**
 * @param {number} score
 * @returns {string}
 */
export function scoreText(score) {
    return (floorNudged(score * 10) / 10).toFixed(1)
}

// weightText writes a part's weight, the points of its full marks, to
// one decimal, a tie rounded up: 200 / 9 reads 22.2, 500 / 9 55.6.
/**
 * @param {number} weight
 * @returns {string}
 */
export function weightText(weight) {
    return weight.toFixed(1)
}

// pointsText writes a part's points to one decimal: at full marks, its
// weight's text; below them, rounded down (floorNudged) and never its
// weight's text, one tenth less where rounding down reaches it, so
// 22.22 points of 22.222… read 22.1.
/**
 * @param {Part} part
 * @returns {string}
 */
export function pointsText(part) {
    const full = weightText(part.weight)
    if (part.sub !== null && part.sub >= 1) return full
    const tenths = floorNudged(part.points * 10)
    const text = (tenths / 10).toFixed(1)
    return text === full ? ((tenths - 1) / 10).toFixed(1) : text
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
