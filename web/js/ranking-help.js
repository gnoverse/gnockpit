// The Ranking view's explanations: the popovers of its headers and of
// its values, from the chain's counts to the points, and the "How is
// this ranked?" dialog (#ranking-modal), whose copy index.html writes
// and whose figures this module fills from the ranking shown. Every
// figure is one /api/ranking publishes: nothing here computes a score.

/** @import { Entry, Part, Published } from '../types/server.js' */
/** @import { PopRow } from './popover.js' */

import { $, mkEl } from './dom.js'
import { keepFocusIn } from './focus.js'
import {
    durationText,
    durationUpText,
    MONTHS,
    pctDown,
    pctUp,
    pointsText,
    scoreText,
    subScoreText,
    thousands,
    weightText,
} from './format.js'
import { shortAddr } from './names.js'
import { popRows } from './popover.js'
import { present } from './present.js'

/** @typedef {'uptime' | 'outages' | 'recovery' | 'proposals'} PartKey */

// ---- The formula's figures
// WEIGHTS are formula v1's weights (ranking/rank.go): the points a
// sub-score of 1 gives before the weights of the sub-scores without
// data are spread over the others. A FormulaVersion change updates
// them, the copy of this module and #ranking-modal's.
/** @type {Readonly<Record<PartKey, number>>} */
export const WEIGHTS = { uptime: 50, outages: 20, recovery: 20, proposals: 10 }
// MIN_HOURS is the block time in the set within the window that
// ranks an operator.
export const MIN_HOURS = 168
// The score with every sub-score at 1.
const MAX_SCORE = 100
const DAY_MS = 86400000
// The parts of a score, in the order the view shows them, and their
// names.
/** @type {readonly PartKey[]} */
const PART_KEYS = ['uptime', 'outages', 'recovery', 'proposals']
/** @type {Readonly<Record<PartKey, string>>} */
const PART_NAMES = {
    uptime: 'Uptime',
    outages: 'Outages',
    recovery: 'Recovery',
    proposals: 'Proposals',
}
// The swatches of the counts that sign and that miss.
const SIGNED = 'var(--green)'
const MISSED = 'var(--red)'

// HEAD_HELP are the Ranking table's header popovers: what each
// column counts, its mapping and its weight; the rank's, the order
// and its ties.
export const HEAD_HELP = {
    rank: 'Order: the higher score, then the higher uptime, then the shorter longest absence (its longest run of missed blocks in the window, recovery runs left out), then the faster recovery (no restart sample last), then the lower missed-proposal share (skipped while proposals are disabled), each compared unrounded. Operators equal on all five share a rank (1, 1, 3): neither an address nor a name decides between them. Two gnockpits reading the same chain, with the same exclusion list, print the same order, unless a valoper profile or a key link changed between their computations.',
    name: "The operator, ranked across its signing keys: a key rotation keeps its record. Keys are linked to operators by chain records only (the operator's valoper profile and the validator set's events), never by name; a key no record links is ranked on its own. Only the operators in the validator set at the window's last height are listed; one that left keeps its record on its validator page.",
    score: '0 to 100, shown to one decimal, rounded down: 50 % uptime, 20 % outages, 20 % recovery, 10 % proposals, each scored 0 to 1. A metric without data (such as no restart sample, no proposal due, or proposals disabled) passes its weight to the others in proportion to theirs.',
    uptime: 'Blocks the validator signed, of the blocks it was in the set for, in the window. A nil vote counts as signed; a missing vote or a vote for another block does not. The blocks it missed around a chain restart (its run of misses through or just before it, within 24 h of it) are left out: Recovery times the restart instead. 95 % or less scores 0, 99.9 % or more scores 1, linear between. 50 % of the score.',
    outages:
        'Times the validator missed 10 or more blocks in a row in the window, a run counting once 10 of its blocks fall inside it; blocks missed around a chain restart, within 24 h of it, are left out. Twenty short outages and one long one can miss the same blocks; the first is a setup to fix. None scores 1, 10 or more score 0, linear between. 20 % of the score.',
    recovery:
        'After a chain restart (two consecutive blocks more than 2 minutes apart: a halt, an upgrade or a stall), the time until the validator signs again; one added at the restart, or already in an outage before it, gets no sample. The median of its last 3 samples within the 180 complete UTC days before today, so one bad restart does not decide alone. 10 min or less scores 1, 1 h 0.66, 6 h 0.33, 24 h or more 0, linear between. 20 % of the score.',
    proposals:
        "Rounds the validator was due to propose and did not, of the rounds it was due to propose, counted on the heights it signed, restart heights left out: a validator that is down already loses those blocks through Uptime. Who was due is recomputed from the validator sets' proposer priorities; a block whose header names none of its rounds' recomputed proposers disables this metric for every validator, in every window holding that block, and /api/ranking lists those heights. 0 % scores 1, 5 % or more scores 0, linear between. 10 % of the score.",
}

// ---- Writing the figures
// windowText writes the days of pub's window: Oct 19 – Nov 17 UTC.
/**
 * @param {Published} pub
 * @returns {string}
 */
export function windowText(pub) {
    return dayText(pub.first_day) + ' – ' + dayText(pub.last_day) + ' UTC'
}
// dayText writes a YYYY-MM-DD day, or the day of an RFC 3339 UTC
// time, as its month's short name and its date: Oct 19.
/**
 * @param {string} day
 * @returns {string}
 */
function dayText(day) {
    return (
        present(MONTHS[Number(day.slice(5, 7)) - 1]) +
        ' ' +
        Number(day.slice(8, 10))
    )
}
// minuteText writes an RFC 3339 UTC time as its day and its minute:
// Nov 12, 01:00 UTC.
/**
 * @param {string} iso
 * @returns {string}
 */
function minuteText(iso) {
    return dayText(iso) + ', ' + iso.slice(11, 16) + ' UTC'
}
// inSetHours is entry e's block time in the set within the window,
// in whole hours rounded down: it reads 168 only once e has them.
/**
 * @param {Entry} e
 * @returns {number}
 */
export function inSetHours(e) {
    return Math.floor(e.hours_in_set)
}
// blocksText writes a count of blocks.
/**
 * @param {number} n
 * @returns {string}
 */
function blocksText(n) {
    return thousands(n) + (n === 1 ? ' block' : ' blocks')
}
// pointsOfWeight writes a part's points of its weight: 41.6 of 62.5.
/**
 * @param {Part} part
 * @returns {string}
 */
function pointsOfWeight(part) {
    return pointsText(part) + ' of ' + weightText(part.weight)
}
// nameOf names operator op of pub: its moniker, else its address.
/**
 * @param {string} op
 * @param {Published} pub
 * @returns {string}
 */
function nameOf(op, pub) {
    const e = pub.entries.find(function (x) {
        return x.operator === op
    })
    return (e && e.name) || shortAddr(op)
}

// ---- Popovers
// partHelp gives el the popover of entry e's value of key in pub:
// the chain's counts behind it, its sub-score and its points.
/**
 * @param {Element} el
 * @param {PartKey} key
 * @param {Entry} e
 * @param {Published} pub
 */
export function partHelp(el, key, e, pub) {
    const help = PART_HELP[key](e, pub)
    popRows(el, help.title, help.rows)
}
/** @typedef {{ title: string, rows: PopRow[] }} Help */
/** @type {Readonly<Record<PartKey, (e: Entry, pub: Published) => Help>>} */
const PART_HELP = {
    uptime: uptimeHelp,
    outages: outagesHelp,
    recovery: recoveryHelp,
    proposals: proposalsHelp,
}
// scoredRows end a value's popover: its sub-score, on its mapping,
// and its points; without a sub-score, none and why, and where its
// weight goes.
/**
 * @param {Part} part
 * @param {PartKey} key
 * @param {string} mapping
 * @param {string} none
 * @returns {PopRow[]}
 */
function scoredRows(part, key, mapping, none) {
    if (part.sub === null)
        return [
            [null, 'Sub-score', none],
            [
                null,
                'Its ' + WEIGHTS[key] + ' %',
                'goes to the other metrics in proportion',
            ],
        ]
    return subRows(part, part.sub, mapping)
}
// subRows end the popover of a value whose sub-score is sub: the
// sub-score, on its mapping, and the part's points.
/**
 * @param {Part} part
 * @param {number} sub
 * @param {string} mapping
 * @returns {PopRow[]}
 */
function subRows(part, sub, mapping) {
    return [
        [null, 'Sub-score (' + mapping + ')', subScoreText(sub)],
        [null, 'Points', pointsOfWeight(part)],
    ]
}
// uptimeHelp: the blocks in the set, signed and missed, the recovery
// runs left out, the blocks counted and the uptime of those, rounded
// down as the cell's.
/**
 * @param {Entry} e
 * @param {Published} pub
 * @returns {Help}
 */
function uptimeHelp(e, pub) {
    const i = e.inputs
    const counted = i.in_set - i.recovering
    /** @type {PopRow[]} */
    const rows = [
        [null, 'Blocks in the set', thousands(i.in_set)],
        [SIGNED, 'Signed', thousands(i.signed)],
        [null, 'of which nil votes', thousands(i.nil)],
        [MISSED, 'Missing from the commit', thousands(i.absent)],
        [MISSED, 'Voted for another block', thousands(i.other_block)],
    ]
    if (i.recovering > 0)
        rows.push([
            null,
            'Left out: missed around restarts',
            '−' + thousands(i.recovering),
        ])
    rows.push([null, 'Counted', thousands(counted)])
    if (counted > 0)
        rows.push([
            null,
            'Uptime',
            pctDown(i.signed, counted, 3).toFixed(3) + ' %',
        ])
    rows.push(
        ...scoredRows(
            e.uptime,
            'uptime',
            '95 % → 0, 99.9 % → 1',
            'none without a counted block'
        )
    )
    return { title: 'Uptime, ' + windowText(pub), rows: rows }
}
// outagesHelp: each outage the window counts, from its first block,
// with its blocks and its length; the longest absence.
/**
 * @param {Entry} e
 * @param {Published} pub
 * @returns {Help}
 */
function outagesHelp(e, pub) {
    const i = e.inputs
    /** @type {PopRow[]} */
    const rows = i.outages.map(function (run) {
        return [
            MISSED,
            minuteText(run.first_time),
            blocksText(run.last - run.first + 1) +
                ' · ' +
                durationText(
                    Date.parse(run.end_time) - Date.parse(run.first_time)
                ),
        ]
    })
    if (!rows.length)
        rows.push([null, 'Runs of 10 or more missed blocks', 'none'])
    rows.push([null, 'Longest absence', blocksText(i.longest_absence)])
    // Every entry has an outages sub-score: a count of outages, none
    // included, always maps to one (ranking/rank.go, outagesSub). An
    // answer without one breaks the server's contract: present throws,
    // and the view says it cannot show the ranking.
    const sub = present(e.outages.sub ?? undefined)
    rows.push(...subRows(e.outages, sub, '0 → 1, 10 → 0'))
    return { title: 'Outages, ' + windowText(pub), rows: rows }
}
// recoveryHelp: each sample the median takes, by its restart, and the
// median, rounded up as the cell's; or no sample.
/**
 * @param {Entry} e
 * @returns {Help}
 */
function recoveryHelp(e) {
    const i = e.inputs
    /** @type {PopRow[]} */
    const rows = i.recovery.map(function (s) {
        return [
            null,
            dayText(s.restart_time) +
                ', restart after block ' +
                thousands(s.restart_height),
            durationUpText(s.duration_ms),
        ]
    })
    let title = 'Recovery: no sample yet'
    if (i.recovery_median_ms === null)
        rows.push([
            null,
            'Samples within 180 days',
            'none: no restart it was signing before and signed again after',
        ])
    else {
        rows.push([null, 'Median', durationUpText(i.recovery_median_ms)])
        const n = i.recovery.length
        title =
            'Recovery, its last ' +
            (n === 1 ? 'restart' : n + ' restarts') +
            ' within 180 days'
    }
    rows.push(
        ...scoredRows(
            e.recovery,
            'recovery',
            '10 min → 1, 1 h → 0.66, 6 h → 0.33, 24 h → 0',
            'none without a sample'
        )
    )
    return { title: title, rows: rows }
}
// proposalsHelp: the rounds it was due to propose on the heights it
// signed, those it proposed and missed, and the missed share, rounded
// up: a miss never reads none; the heights that disable the sub-score.
/**
 * @param {Entry} e
 * @param {Published} pub
 * @returns {Help}
 */
function proposalsHelp(e, pub) {
    const i = e.inputs
    const due = i.proposals_expected
    /** @type {PopRow[]} */
    const rows = [
        [null, 'Due to propose, on heights it signed', thousands(due)],
        [SIGNED, 'Proposed', thousands(due - i.proposals_missed)],
        [MISSED, 'Missed', thousands(i.proposals_missed)],
    ]
    if (due > 0)
        rows.push([
            null,
            'Missed share',
            pctUp(i.proposals_missed, due, 2).toFixed(2) + ' %',
        ])
    const flagged = pub.proposals_disabled.length
    const none = flagged
        ? 'disabled: ' +
          thousands(flagged) +
          (flagged === 1 ? ' height' : ' heights') +
          ' whose proposer could not be checked'
        : 'none without a proposal due'
    rows.push(...scoredRows(e.proposals, 'proposals', '0 % → 1, 5 % → 0', none))
    return { title: 'Proposals, ' + windowText(pub), rows: rows }
}
// scoreHelp gives el the popover of the score of entry e of pub,
// ranked or excluded: its four parts and the score; ranked, its rank
// and the operators sharing it.
/**
 * @param {Element} el
 * @param {Entry} e
 * @param {Published} pub
 */
export function scoreHelp(el, e, pub) {
    /** @type {PopRow[]} */
    const rows = PART_KEYS.map(function (key) {
        const part = e[key]
        return [
            null,
            PART_NAMES[key],
            part.sub === null
                ? 'no data: its weight spread over the others'
                : pointsOfWeight(part),
        ]
    })
    rows.push([null, 'Score', scoreText(e.score) + ' of ' + MAX_SCORE])
    if (e.tied_with.length)
        rows.push([
            null,
            'Rank shared with',
            e.tied_with
                .map(function (op) {
                    return nameOf(op, pub)
                })
                .join(', ') + ': equal on the score and every tie-break',
        ])
    if (e.status !== 'ranked') {
        popRows(el, "Not ranked: on this dashboard's exclusion list", rows)
        return
    }
    const ranked = pub.entries.filter(function (x) {
        return x.status === 'ranked'
    }).length
    popRows(el, 'Rank ' + e.rank + ' of ' + ranked + ' ranked operators', rows)
}
// waitHelp gives el the popover of entry e of pub, not ranked yet:
// its hours in the set, the hours needed, and the day it would be
// ranked from if it stays in the set, each day ranked adding the
// day before to its window.
/**
 * @param {Element} el
 * @param {Entry} e
 * @param {Published} pub
 */
export function waitHelp(el, e, pub) {
    const days = Math.ceil((MIN_HOURS - e.hours_in_set) / 24)
    const from = new Date(Date.parse(pub.day) + days * DAY_MS)
    popRows(el, 'Not ranked yet', [
        [null, 'In the set within the window', inSetHours(e) + ' h'],
        [null, 'Needed to be ranked', MIN_HOURS + ' h (7 days)'],
        [
            null,
            'Ranked from about',
            dayText(from.toISOString()) + ' (if it stays in the set)',
        ],
    ])
}
// hiddenHelp gives el the popover of the excluded operators the view
// hides: each one's name and address, or its address alone when it
// has no name.
/**
 * @param {Element} el
 * @param {Entry[]} hidden
 */
export function hiddenHelp(el, hidden) {
    popRows(
        el,
        "Not ranked: this dashboard's exclusion list",
        hidden.map(function (e) {
            const addr = shortAddr(e.operator)
            /** @type {PopRow} */
            const row = e.name ? [null, e.name, addr] : [null, addr, '']
            return row
        })
    )
}

// ---- How the ranking works
// explainRanking fills the dialog with the figures of the ranking
// pub, which the view shows: its window, its heights and its formula.
/** @param {Published} pub */
export function explainRanking(pub) {
    $('ranking-window').textContent = windowText(pub)
    $('ranking-heights').textContent =
        pub.last_height > 0
            ? ', heights ' +
              thousands(pub.first_height) +
              ' to ' +
              thousands(pub.last_height)
            : ', which holds no block'
    $('ranking-formula').textContent = String(pub.formula)
}
// howButton is the meta line's "How is this ranked?": a click on it
// opens the dialog (start listens for it).
/** @returns {HTMLButtonElement} */
export function howButton() {
    return mkEl('button', 'How is this ranked?', 'rk-how', { type: 'button' })
}

export function start() {
    const modal = $('ranking-modal')
    $('ranking-origin').textContent = location.origin
    // close closes the dialog and gives the focus back to the button
    // that opens it, the one the view shows: a read that changed the
    // ranking meanwhile drew it anew.
    function close() {
        modal.classList.remove('open')
        const how = document.querySelector('button.rk-how')
        if (how instanceof HTMLElement) how.focus()
    }
    // A click on "How is this ranked?" opens the dialog, the focus on
    // its Close button. One listener serves every button the view
    // draws.
    document.addEventListener('click', function (e) {
        if (!(e.target instanceof Element)) return
        if (!e.target.closest('button.rk-how')) return
        modal.classList.add('open')
        $('ranking-close').focus()
    })
    $('ranking-close').addEventListener('click', close)
    modal.addEventListener('click', function (e) {
        if (e.target === modal) close()
    })
    document.addEventListener('keydown', function (e) {
        if (!modal.classList.contains('open')) return
        if (e.key === 'Escape') close()
        if (e.key === 'Tab') keepFocusIn(modal, e)
    })
}
