// The validators card's Ranking view: the daily validator ranking
// /api/ranking serves, read when the view shows and again every 60 s
// while it does. Its table shows on a wide screen, its cards up to
// 768 px, as the Live view's: the stylesheet decides.

/** @import { Entry, Published, RankingAnswer, State } from '../types/server.js' */
/** @import { PartKey } from './ranking-help.js' */

import { $, mkEl } from './dom.js'
import { focusPlace, focusPlaceIn } from './focus.js'
import {
    durationText,
    durationUpText,
    pctDown,
    pointsText,
    scoreText,
    sharePctDown,
    thousands,
} from './format.js'
import { shortAddr, valLink } from './names.js'
import { present } from './present.js'
import {
    explainRanking,
    HEAD_HELP,
    hiddenHelp,
    howButton,
    inSetHours,
    MIN_HOURS,
    partHelp,
    scoreHelp,
    waitHelp,
    WEIGHTS,
    windowText,
} from './ranking-help.js'
import { boundedRead, failureMessage } from './read.js'

// show and hide are what rankingShown and rankingHidden do: start
// sets them, as the view's reads live in start. Before start, or
// after a start that failed, they do nothing.
let show = function () {}
let hide = function () {}

// rankingShown reads the ranking at once, and again every 60 s
// until rankingHidden.
export function rankingShown() {
    show()
}

// rankingHidden stops the view's reads, giving up the one in flight.
export function rankingHidden() {
    hide()
}

export function start() {
    const POLL_MS = 60000
    // A read without an answer by then is given up, so a stalled one
    // never holds up the reads after it.
    const READ_BOUND_MS = 10000
    // The note heading each group of entries after the ranked ones.
    /** @type {Record<Exclude<Entry['status'], 'ranked'>, string>} */
    const SEPARATORS = {
        excluded:
            "Not ranked: run by the gno.land team or close partners (this dashboard's exclusion list). Their values are shown, scored the same way.",
        not_ranked_yet:
            'Not ranked yet: less than ' +
            MIN_HOURS +
            ' h (7 days) in the set within the window',
    }
    const panel = $('validators-ranking')
    let gen = 0 // the read chain in force: showing the view starts another
    let timer = 0
    // chain ends the chain in force: showing or hiding the view aborts
    // it, giving up its read in flight.
    let chain = new AbortController()
    /** @type {RankingAnswer | null} */
    let answer = null // the last answer read; null before one
    let loadErr = '' // why the last read failed; '' when it did not
    // shown is what the panel shows, as the answer and the error it
    // was drawn from: a read that changes neither leaves the panel,
    // and the focus in it, as it is.
    let shown = ''

    // load reads the ranking. The read is given up past the bound,
    // or once ends, its chain's signal, aborts.
    /**
     * @param {AbortSignal} ends
     * @returns {Promise<RankingAnswer>}
     */
    function load(ends) {
        return boundedRead(READ_BOUND_MS, async function (signal) {
            const read = new AbortController()
            const abort = function () {
                read.abort()
            }
            signal.addEventListener('abort', abort, { once: true })
            ends.addEventListener('abort', abort, { once: true })
            try {
                const r = await fetch('/api/ranking', { signal: read.signal })
                if (!r.ok) throw new Error('HTTP ' + r.status)
                /** @type {RankingAnswer} */
                const a = await r.json()
                return a
            } finally {
                // A chain lives through many reads.
                ends.removeEventListener('abort', abort)
            }
        })
    }
    // refresh reads the ranking, asks for the next read in POLL_MS,
    // and shows it, while g is the chain in force; ends is the
    // chain's signal. A failed read keeps what the panel shows and
    // says so; the next read tries again. It never rejects: render
    // says what it cannot draw.
    /**
     * @param {number} g
     * @param {AbortSignal} ends
     */
    async function refresh(g, ends) {
        try {
            const a = await load(ends)
            if (g !== gen) return
            answer = a
            loadErr = ''
        } catch (e) {
            if (g !== gen) return
            loadErr =
                'Could not read the ranking (' +
                failureMessage(e) +
                '): trying again shortly.'
        }
        timer = setTimeout(function () {
            refresh(g, ends)
        }, POLL_MS)
        render()
    }
    show = function () {
        clearTimeout(timer)
        chain.abort()
        chain = new AbortController()
        gen++
        refresh(gen, chain.signal)
    }
    hide = function () {
        clearTimeout(timer)
        chain.abort()
        gen++
    }

    // render shows the last answer and the last read's error, unless
    // the panel shows them already, the keyboard's focus kept on the
    // control it was on (data-key: the meta line's controls, the
    // header row, each operator's row and card). An answer that
    // breaks the server's contract, which it cannot draw, it replaces
    // with a line saying so.
    function render() {
        const now = loadErr + '\n' + JSON.stringify(answer)
        if (now === shown) return
        /** @type {HTMLElement[]} */
        let out
        try {
            out = drawn()
        } catch (e) {
            out = [
                mkEl(
                    'div',
                    'Could not show the ranking (' + failureMessage(e) + ').',
                    'rk-err'
                ),
            ]
        }
        const focused = focusPlaceIn(panel)
        panel.replaceChildren(...out)
        focusPlace(focused)
        shown = now
    }
    // drawn builds the panel's content: the last read's error, the
    // ranking's state, and the ranking published.
    /** @returns {HTMLElement[]} */
    function drawn() {
        /** @type {HTMLElement[]} */
        const out = []
        if (loadErr) out.push(mkEl('div', loadErr, 'rk-err'))
        if (answer) {
            const line = stateLine(answer.state, answer.ranking)
            if (line) out.push(line)
            if (answer.ranking)
                out.push(...rankingOf(answer.ranking, answer.hide_excluded))
        }
        return out
    }

    // ---- States
    // stateLine says why no ranking can be published for today: the
    // history it needs is filling, or out of the node's reach; or,
    // ready, that today's is not published yet. Null while ready
    // with a ranking published.
    /**
     * @param {State} st
     * @param {Published | null} pub
     * @returns {HTMLElement | null}
     */
    function stateLine(st, pub) {
        if (st.kind === 'filling') return fillingLine(st)
        if (st.kind === 'unavailable')
            return mkEl(
                'div',
                'This node keeps no blocks below #' +
                    // An unavailable state carries its floor.
                    thousands(present(st.floor)) +
                    ', and the ranking needs the last 180 days of blocks.',
                'rk-state'
            )
        if (!pub)
            return mkEl(
                'div',
                'The ranking is being computed for today.',
                'rk-state'
            )
        return null
    }
    // fillingLine writes the share of the chain's history stored,
    // and the time left at the fill's rate, when known: an upper
    // bound, counted to genesis; once the blocks are stored, the
    // set changes' events still to read.
    /**
     * @param {State} st
     * @returns {HTMLElement}
     */
    function fillingLine(st) {
        const bar = mkEl('span', null, 'rk-bar')
        const fill = mkEl('i')
        fill.style.width = st.stored_share * 100 + '%'
        bar.appendChild(fill)
        let stored = sharePctDown(st.stored_share) + ' %'
        if (st.events_pending)
            stored += " · reading the validator set's changes"
        else if (st.left_s > 0)
            stored += ' · up to ' + durationText(st.left_s * 1000) + ' left'
        const line = mkEl('div', null, 'rk-state')
        line.append(
            'The ranking needs the blocks of the last 180 days (back to genesis on a younger chain): recovery after restarts looks back that far. Stored so far: ',
            bar,
            ' ' + stored
        )
        return line
    }

    // ---- The ranking
    // rankingOf builds the ranking pub: its meta line, the table and
    // the cards; the excluded operators left out when hideExcluded,
    // the meta line counting them. A line takes the place of a list
    // with no entry. The dialog explaining the ranking takes pub's
    // figures.
    /**
     * @param {Published} pub
     * @param {boolean} hideExcluded
     * @returns {HTMLElement[]}
     */
    function rankingOf(pub, hideExcluded) {
        const listed = hideExcluded
            ? pub.entries.filter(function (e) {
                  return e.status !== 'excluded'
              })
            : pub.entries
        const hidden = hideExcluded
            ? pub.entries.filter(function (e) {
                  return e.status === 'excluded'
              })
            : []
        explainRanking(pub)
        if (!listed.length)
            return [
                meta(pub, hidden),
                mkEl('div', noneListed(hidden.length), 'rk-state'),
            ]
        return [meta(pub, hidden), table(listed, pub), cards(listed, pub)]
    }
    // noneListed says why a ranking lists no operator: the hidden
    // operators, which the meta line counts, are every one in the set;
    // with none hidden, the window holds no block, so no set either.
    /**
     * @param {number} hidden
     * @returns {string}
     */
    function noneListed(hidden) {
        if (!hidden) return 'No validator to rank: the window holds no block.'
        return 'No community validator is in the set.'
    }
    // meta writes the ranking's window, its heights unless it holds
    // none, its formula, and the count of the excluded operators
    // hidden, unless none is, whose popover names them.
    /**
     * @param {Published} pub
     * @param {Entry[]} hidden
     * @returns {HTMLElement}
     */
    function meta(pub, hidden) {
        const m = mkEl('div', null, 'rk-meta')
        const days =
            Math.round(
                (Date.parse(pub.last_day) - Date.parse(pub.first_day)) /
                    86400000
            ) + 1
        let span = days + ' days, ' + windowText(pub)
        if (pub.last_height > 0)
            span +=
                ' · heights ' +
                thousands(pub.first_height) +
                ' – ' +
                thousands(pub.last_height)
        m.append(
            mkEl('span', span),
            mkEl(
                'span',
                'formula v' + pub.formula + ' · updated daily after 00:00 UTC'
            )
        )
        if (hidden.length) {
            const count = mkEl(
                'button',
                hidden.length +
                    ' team and partner ' +
                    (hidden.length === 1 ? 'validator' : 'validators') +
                    ' not ranked',
                'info-btn',
                { type: 'button', 'data-key': 'hidden' }
            )
            hiddenHelp(count, hidden)
            m.append(count)
        }
        const how = howButton()
        how.setAttribute('data-key', 'how')
        m.append(how)
        return m
    }
    // table builds the table of entries of pub: each header a button
    // whose popover says what its column counts.
    /**
     * @param {Entry[]} entries
     * @param {Published} pub
     * @returns {HTMLElement}
     */
    function table(entries, pub) {
        const head = mkEl('tr', null, null, { 'data-key': 'head' })
        // Each column's label, whether it holds numbers, and its
        // popover.
        /** @type {Array<[string, boolean, string]>} */
        const heads = [
            ['#', false, HEAD_HELP.rank],
            ['Name', false, HEAD_HELP.name],
            ['Score', true, HEAD_HELP.score],
            ['Uptime (/' + WEIGHTS.uptime + ')', true, HEAD_HELP.uptime],
            ['Outages (/' + WEIGHTS.outages + ')', true, HEAD_HELP.outages],
            ['Recovery (/' + WEIGHTS.recovery + ')', true, HEAD_HELP.recovery],
            [
                'Missed props (/' + WEIGHTS.proposals + ')',
                true,
                HEAD_HELP.proposals,
            ],
        ]
        for (const [label, num, help] of heads) {
            const th = mkEl('th', null, num ? 'num' : null)
            th.appendChild(
                mkEl('button', label, 'info-btn', {
                    type: 'button',
                    'data-popover': help,
                })
            )
            head.appendChild(th)
        }
        const thead = mkEl('thead')
        thead.appendChild(head)
        const body = mkEl('tbody')
        eachGroup(
            entries,
            function (status) {
                const tr = mkEl('tr', null, 'rk-sep')
                tr.appendChild(
                    mkEl('td', SEPARATORS[status], null, {
                        colspan: String(heads.length),
                    })
                )
                body.appendChild(tr)
            },
            function (e) {
                body.appendChild(row(e, pub))
            }
        )
        const t = mkEl('table')
        t.append(thead, body)
        const wrap = mkEl('div', null, 'table-wrap rk-wrap')
        wrap.appendChild(t)
        return wrap
    }
    // row builds the row of entry e of pub: its rank, name, score and
    // values; or, not ranked yet, its hours in the set; each but the
    // rank and the name with its popover.
    /**
     * @param {Entry} e
     * @param {Published} pub
     * @returns {HTMLTableRowElement}
     */
    function row(e, pub) {
        const tr = mkEl(
            'tr',
            null,
            e.status === 'excluded' ? 'rk-excluded' : null,
            { 'data-key': 'row ' + e.operator }
        )
        tr.appendChild(mkEl('td', rankText(e), 'rank'))
        const name = mkEl('td')
        name.appendChild(nameLink(e))
        tr.appendChild(name)
        if (e.status === 'not_ranked_yet') {
            const wait = mkEl('td', hoursText(e), 'rk-wait', { colspan: '5' })
            waitHelp(wait, e, pub)
            tr.appendChild(wait)
            return tr
        }
        const score = mkEl('td', scoreText(e.score), 'num score')
        scoreHelp(score, e, pub)
        tr.appendChild(score)
        for (const [key, , raw] of values(e))
            tr.appendChild(valueIn(mkEl('td', null, 'num'), key, e, pub, raw))
        return tr
    }
    /**
     * @param {Entry[]} entries
     * @param {Published} pub
     * @returns {HTMLElement}
     */
    function cards(entries, pub) {
        const box = mkEl('div', null, 'node-cards rk-cards')
        eachGroup(
            entries,
            function (status) {
                box.appendChild(mkEl('div', SEPARATORS[status], 'rk-sep'))
            },
            function (e) {
                box.appendChild(card(e, pub))
            }
        )
        return box
    }
    // card builds the card of entry e of pub: its rank, name and score
    // on top, its values below; or, not ranked yet, its hours in the
    // set; each but the name with its popover. A rank and the values'
    // labels carry their columns' header popovers, which a card has no
    // header to show.
    /**
     * @param {Entry} e
     * @param {Published} pub
     * @returns {HTMLDivElement}
     */
    function card(e, pub) {
        const c = mkEl(
            'div',
            null,
            'peer-card' + (e.status === 'excluded' ? ' rk-excluded' : ''),
            { 'data-key': 'card ' + e.operator }
        )
        const top = mkEl('div', null, 'peer-card-top')
        const name = mkEl('span', null, 'peer-card-name')
        // A rank's popover gives the order; a dash, no rank, has none.
        const rank =
            e.status === 'ranked'
                ? mkEl('button', '#' + rankText(e), 'rk-rank info-btn', {
                      type: 'button',
                      'data-popover': HEAD_HELP.rank,
                  })
                : mkEl('span', rankText(e), 'rk-rank')
        name.append(rank, nameLink(e))
        top.appendChild(name)
        c.appendChild(top)
        if (e.status === 'not_ranked_yet') {
            const wait = mkEl('span', hoursText(e), 'rk-wait')
            waitHelp(wait, e, pub)
            top.appendChild(wait)
            return c
        }
        const score = mkEl('span', scoreText(e.score), 'rk-score')
        scoreHelp(score, e, pub)
        top.appendChild(score)
        const kv = mkEl('div', null, 'peer-card-kv')
        for (const [key, label, raw] of values(e))
            kv.append(
                mkEl('button', label, 'peer-card-k info-btn', {
                    type: 'button',
                    'data-popover': HEAD_HELP[key],
                }),
                valueIn(mkEl('span', null, 'peer-card-v'), key, e, pub, raw)
            )
        c.appendChild(kv)
        return c
    }
    // eachGroup walks entries, in their order: ranked, excluded, not
    // ranked yet. It calls group with the status of each group but
    // the ranked before its first entry, and entry with each entry.
    /**
     * @param {Entry[]} entries
     * @param {(status: Exclude<Entry['status'], 'ranked'>) => void} group
     * @param {(e: Entry) => void} entry
     */
    function eachGroup(entries, group, entry) {
        /** @type {Entry['status']} */
        let status = 'ranked'
        for (const e of entries) {
            if (e.status !== status && e.status !== 'ranked') group(e.status)
            status = e.status
            entry(e)
        }
    }
    // rankText writes entry e's rank, a dash unless ranked.
    /**
     * @param {Entry} e
     * @returns {string}
     */
    function rankText(e) {
        return e.status === 'ranked' ? String(e.rank) : '—'
    }
    // nameLink links the page of entry e's operator, named by its
    // moniker, else by its address.
    /**
     * @param {Entry} e
     * @returns {HTMLAnchorElement}
     */
    function nameLink(e) {
        return valLink(e.operator, e.name || shortAddr(e.operator))
    }
    /**
     * @param {Entry} e
     * @returns {string}
     */
    function hoursText(e) {
        return inSetHours(e) + ' h of ' + MIN_HOURS + ' h in the set'
    }
    // values are entry e's scored values: each one's part of the
    // score, its label, and its raw value as written, which valueIn
    // leaves out for a part without a sub-score: '' when there is no
    // value to write. None reads better than it is: the uptime is
    // rounded down, so it reads 99.90 % only at or past the anchor of
    // full marks its colour reflects, and the recovery median up.
    /**
     * @param {Entry} e
     * @returns {Array<[PartKey, string, string]>}
     */
    function values(e) {
        const i = e.inputs
        const counted = i.in_set - i.recovering
        return [
            [
                'uptime',
                'uptime',
                counted > 0
                    ? pctDown(i.signed, counted, 2).toFixed(2) + ' %'
                    : '',
            ],
            ['outages', 'outages', String(i.outages.length)],
            [
                'recovery',
                'recovery',
                i.recovery_median_ms === null
                    ? ''
                    : durationUpText(i.recovery_median_ms),
            ],
            [
                'proposals',
                'missed props',
                thousands(i.proposals_missed) +
                    ' / ' +
                    thousands(i.proposals_expected),
            ],
        ]
    }
    // valueIn fills el with the value of key of entry e of pub: raw,
    // coloured by its sub-score (green at full marks, red at zero,
    // orange between), then its points, its weight's text only at
    // full marks (pointsText); a dash alone for a value
    // without sub-score, whose weight the others share. Its popover
    // goes from the chain's counts to the points.
    /**
     * @template {HTMLElement} T
     * @param {T} el
     * @param {PartKey} key
     * @param {Entry} e
     * @param {Published} pub
     * @param {string} raw
     * @returns {T}
     */
    function valueIn(el, key, e, pub, raw) {
        const part = e[key]
        partHelp(el, key, e, pub)
        if (part.sub === null) {
            el.appendChild(mkEl('span', '—', 'rk-val'))
            return el
        }
        const band = part.sub >= 1 ? 'ok' : part.sub <= 0 ? 'err' : 'warn'
        el.append(
            mkEl('span', raw, 'rk-val ' + band),
            mkEl('span', ' (' + pointsText(part) + ')', 'rk-pts')
        )
        return el
    }
}
