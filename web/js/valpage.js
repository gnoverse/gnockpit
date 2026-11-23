// A validator's page opens in a modal over the dashboard, from a
// click on its name (validators table, phone card, Timeline view)
// or from ?val= in the URL, and reads /api/validator. Opening
// pushes ?val=, so Back closes it; closing restores the URL.

/** @import { TimelineEntry, ValidatorDay, ValidatorRecovery, ValidatorReport } from '../types/validator.js' */
/** @import { Band } from './format.js' */
/** @import { PopRow } from './popover.js' */

import { copyNotify } from './copy.js'
import { countryFlag, countryName } from './countries.js'
import { $, clearChildren, mkEl, mkLink, SVG_NS } from './dom.js'
import { focusable, keepFocusIn } from './focus.js'
import {
    scoreBand,
    thousands,
    timeAgo,
    twoDigits,
    utcMinute,
    voteDelayText,
} from './format.js'
import { shortAddr } from './names.js'
import { hidePopover, popRows, showPopover } from './popover.js'
import { present } from './present.js'
import { boundedRead, errorOf, failureMessage } from './read.js'

// pushedEntry says whether the current history entry is one the page
// pushed: it carries the { val } state the page gives its own entries.
// history.state is typed any, so it is read as unknown.
/** @returns {boolean} */
function pushedEntry() {
    /** @type {unknown} */
    const state = history.state
    return (
        typeof state === 'object' &&
        state !== null &&
        'val' in state &&
        !!state.val
    )
}

/** @typedef {Extract<TimelineEntry, { kind: 'outage' }>} OutageEntry */
/**
 * A UTC day of the charts: its date and its start in ms since the
 * epoch, its counters (null without blocks in the set), and the
 * outages that cover it.
 * @typedef {object} ChartDay
 * @property {string} day
 * @property {number} t
 * @property {ValidatorDay | null} c
 * @property {OutageEntry[]} outages
 */
/**
 * A day series: each day's fill and popover rows, and whether a day
 * with an outage gets a dot.
 * @typedef {object} Series
 * @property {(d: ChartDay) => string} fill
 * @property {(d: ChartDay) => PopRow[]} rows
 * @property {boolean} dots
 */
/**
 * A chart and its legend, drawn from the days, each px wide, of report
 * rep; cal asks a phone's calendar of the series that has one.
 * @typedef {(
 *     days: ChartDay[],
 *     px: number,
 *     rep: ValidatorReport,
 *     cal: boolean
 * ) => { chart: Element, legend: HTMLElement }} ChartDraw
 */
/**
 * A section's stats in a window: its name, then [label, value, band
 * class or ''] per stat.
 * @typedef {[string, Array<[string, string, string]>]} StatsWindow
 */

export function start() {
    const modal = $('val-modal')
    const body = $('val-body')
    const title = $('val-title')
    /** @type {string | null} */
    let shown = null // the ?val= the page shows; null while closed
    let pushed = false // the URL's ?val= is an entry this page pushed
    let seq = 0 // the read in force: a newer one drops older answers
    // A read of a report without an answer by then is given up.
    const REPORT_READ_BOUND_MS = 10000
    /** @type {Element | null} */
    let opener = null // focused before opening, focused again on close
    let openerAt = 0 // its place among the shown links to its validator

    // ---- Formats
    // dur writes a duration: seconds kept under an hour, days from
    // 48 h.
    /**
     * @param {number} ms
     * @returns {string}
     */
    function dur(ms) {
        const s = Math.round(ms / 1000)
        if (s < 60) return s + ' s'
        if (s < 3600)
            return Math.floor(s / 60) + ' min ' + twoDigits(s % 60) + ' s'
        const h = Math.floor(s / 3600)
        if (h >= 48) return Math.round(h / 24) + ' d'
        return h + ' h ' + twoDigits(Math.floor((s % 3600) / 60)) + ' min'
    }
    // shownPct is n of total as a percentage rounded to digits
    // decimals from the exact counts, a tie rounding up: the
    // figure the page writes and its band reads, so 99.895 % is
    // written 99.90 % and banded as 99.90 %.
    /**
     * @param {number} n
     * @param {number} total
     * @param {number} digits
     * @returns {number}
     */
    function shownPct(n, total, digits) {
        const scale = 10 ** digits
        return Math.round((n * 100 * scale) / total) / scale
    }
    // missedShare writes missed of expected proposals as a share
    // to the tenth, with its band. A miss is never none missed:
    // one whose share rounds to 0.0 % reads < 0.1 % and takes
    // the band of a share under 5 %.
    /**
     * @param {number} missed
     * @param {number} expected
     * @returns {{ text: string, band: Band }}
     */
    function missedShare(missed, expected) {
        const pct = shownPct(missed, expected, 1)
        if (missed > 0 && pct === 0) return { text: '< 0.1 %', band: 'warn' }
        return {
            text: pct.toFixed(1) + ' %',
            band: scoreBand.proposals(pct),
        }
    }
    // recoveryBand bands a recovery by its duration in whole
    // seconds, as dur writes it: 600.4 s, written 10 min 00 s,
    // is within 10 min.
    /**
     * @param {number} ms
     * @returns {Band}
     */
    function recoveryBand(ms) {
        return scoreBand.recovery(Math.round(ms / 1000) * 1000)
    }
    // ---- Building blocks
    // info builds an ⓘ whose popover says what a datum is, where it
    // comes from, and whether the ranking scores it.
    /**
     * @param {string} what
     * @param {string} source
     * @param {string} scored
     * @returns {HTMLSpanElement}
     */
    function info(what, source, scored) {
        const i = mkEl('span', 'ⓘ', 'info-btn', { tabindex: '0' })
        i.setAttribute(
            'data-popover',
            'What: ' +
                what +
                '\nSource: ' +
                source +
                '\nScored: ' +
                (scored ? 'yes, ' + scored : 'no, shown for monitoring')
        )
        return i
    }
    // section is a section of the page: its title, then its ⓘ.
    /**
     * @param {string} key
     * @param {string} name
     * @param {HTMLElement} i
     * @returns {HTMLDivElement}
     */
    function section(key, name, i) {
        const sec = mkEl('div', null, 'val-sec', {
            'data-section': key,
        })
        const h = mkEl('h3', name)
        h.appendChild(i)
        sec.appendChild(h)
        return sec
    }
    // statsLine builds a section's stats: [window, [[label, value,
    // band class or '']…]] per window.
    /**
     * @param {StatsWindow[]} windows
     * @returns {HTMLDivElement}
     */
    function statsLine(windows) {
        const line = mkEl('div', null, 'val-stats')
        windows.forEach(function (w) {
            const win = mkEl('span', null, 'val-win')
            win.appendChild(mkEl('span', w[0], 'w'))
            w[1].forEach(function (st) {
                const item = mkEl('span')
                item.appendChild(mkEl('span', st[0], 'l'))
                item.appendChild(mkEl('b', st[1], st[2]))
                win.appendChild(item)
            })
            line.appendChild(win)
        })
        return line
    }
    // chart is the box of the chart named key, which drawCharts
    // fills.
    /**
     * @param {string} key
     * @returns {HTMLDivElement}
     */
    function chart(key) {
        return mkEl('div', null, 'val-chart', { 'data-chart': key })
    }

    // ---- Charts
    // One day axis for every chart on a wide screen: a day is
    // max(DAY_PX, width ÷ days) wide, month ticks under it; the
    // axes scroll together and open on the newest day. On a phone,
    // Uptime and Proposals are calendars.
    const DAY_PX = 8
    const DAY_MS = 86400000
    const NO_HISTORY = 'nothing in the block history yet'
    const EMPTY = 'var(--border)'
    /** @type {Record<Band, string>} */
    const BAND_FILL = {
        ok: 'var(--green)',
        warn: 'var(--yellow)',
        err: 'var(--red)',
    }
    const DELAY_FILLS = [
        '#484f58',
        '#6e7681',
        '#2ea043',
        '#7ee787',
        '#d29922',
        '#f0883e',
        '#f85149',
    ]
    /** @type {ValidatorReport | null} */
    let chartRep = null // the report the charts draw; null while closed
    let chartWidth = 0 // the window width they were drawn at

    // svgEl builds an SVG element of tag with its attributes.
    /**
     * @template {keyof SVGElementTagNameMap} K
     * @param {K} tag
     * @param {Record<string, string | number>} attrs
     * @returns {SVGElementTagNameMap[K]}
     */
    function svgEl(tag, attrs) {
        const e = document.createElementNS(SVG_NS, tag)
        for (const k in attrs) e.setAttribute(k, String(attrs[k]))
        return e
    }
    // svgRoot is an svg w by h px.
    /**
     * @param {number} w
     * @param {number} h
     * @returns {SVGSVGElement}
     */
    function svgRoot(w, h) {
        return svgEl('svg', {
            width: w,
            height: h,
            viewBox: '0 0 ' + w + ' ' + h,
        })
    }
    // filled fills e with colour, and returns it.
    /**
     * @template {SVGElement} E
     * @param {E} e
     * @param {string} colour
     * @returns {E}
     */
    function filled(e, colour) {
        e.style.fill = colour
        return e
    }
    // axisText is axis label s at (x, y).
    /**
     * @param {number} x
     * @param {number} y
     * @param {string} s
     * @returns {SVGTextElement}
     */
    function axisText(x, y, s) {
        const t = filled(
            svgEl('text', { x: x, y: y, 'font-size': 10 }),
            'var(--fg2)'
        )
        t.textContent = s
        return t
    }
    // dayStart is the start of ms's UTC day; month names day's
    // month, day a YYYY-MM-DD date.
    /**
     * @param {number} ms
     * @returns {number}
     */
    function dayStart(ms) {
        return Math.floor(ms / DAY_MS) * DAY_MS
    }
    /**
     * @param {string} day
     * @returns {string}
     */
    function month(day) {
        return new Date(day + 'T00:00:00Z').toUTCString().slice(8, 11)
    }
    // DAY_LABEL_PX is the day width from which each day gets its
    // own date under the axis: a short range has no month start.
    const DAY_LABEL_PX = 40
    // monthAxis draws the day axis at y under days drawn px wide: a
    // tick and a label at each month's first day, the first
    // month's name at the left when no month starts near it, and
    // each day's date when days are DAY_LABEL_PX wide or more.
    /**
     * @param {SVGSVGElement} svg
     * @param {ChartDay[]} days
     * @param {number} px
     * @param {number} y
     */
    function monthAxis(svg, days, px, y) {
        const line = svgEl('line', {
            x1: 0,
            x2: days.length * px,
            y1: y,
            y2: y,
        })
        line.style.stroke = 'var(--border)'
        svg.appendChild(line)
        let firstStart = -1
        days.forEach(function (d, i) {
            if (d.day.slice(8) !== '01') return
            if (firstStart < 0) firstStart = i
            const tick = svgEl('line', {
                x1: i * px,
                x2: i * px,
                y1: y,
                y2: y + 4,
            })
            tick.style.stroke = 'var(--fg2)'
            svg.appendChild(tick)
        })
        if (px >= DAY_LABEL_PX) {
            days.forEach(function (d, i) {
                let date = String(Number(d.day.slice(8)))
                if (i === 0 || d.day.slice(8) === '01')
                    date += ' ' + month(d.day)
                svg.appendChild(axisText(i * px + 2, y + 14, date))
            })
            return
        }
        days.forEach(function (d, i) {
            if (d.day.slice(8) === '01')
                svg.appendChild(axisText(i * px + 2, y + 14, month(d.day)))
        })
        if (firstStart < 0 || firstStart * px > DAY_LABEL_PX)
            svg.appendChild(axisText(2, y + 14, month(present(days[0]).day)))
    }

    // chartDays lists the UTC days from the report's first to its
    // today, the day after the scoring window's last: each with
    // its counters (null without blocks in the set) and the
    // outages that cover it.
    /**
     * @param {ValidatorReport} rep
     * @returns {ChartDay[]}
     */
    function chartDays(rep) {
        if (!rep.days.length) return []
        /** @type {Record<string, ValidatorDay>} */
        const byDay = {}
        rep.days.forEach(function (c) {
            byDay[c.day] = c
        })
        /** @type {Record<string, OutageEntry[]>} */
        const outages = {}
        rep.timeline.forEach(function (e) {
            if (e.kind !== 'outage') return
            // Its last absence is before block to+1's time; an
            // ongoing one's is the newest block, at end_time.
            const last = dayStart(Date.parse(e.end_time) - (e.ongoing ? 0 : 1))
            for (let t = dayStart(Date.parse(e.time)); t <= last; t += DAY_MS) {
                const day = new Date(t).toISOString().slice(0, 10)
                ;(outages[day] = outages[day] || []).push(e)
            }
        })
        const today = Date.parse(rep.summary.last_day + 'T00:00:00Z') + DAY_MS
        /** @type {ChartDay[]} */
        const out = []
        for (
            let t = Date.parse(present(rep.days[0]).day + 'T00:00:00Z');
            t <= today;
            t += DAY_MS
        ) {
            const day = new Date(t).toISOString().slice(0, 10)
            out.push({
                day: day,
                t: t,
                c: byDay[day] && byDay[day].in_set ? byDay[day] : null,
                outages: outages[day] || [],
            })
        }
        return out
    }

    // uptimeFill and uptimeRows are the Uptime series' fill and rows
    // of day d; proposalsFill and proposalsRows the Proposals'.
    /**
     * @param {ChartDay} d
     * @returns {string}
     */
    function uptimeFill(d) {
        if (!d.c) return EMPTY
        return BAND_FILL[scoreBand.uptime(shownPct(d.c.signed, d.c.in_set, 2))]
    }
    /**
     * @param {ChartDay} d
     * @returns {PopRow[]}
     */
    function uptimeRows(d) {
        if (!d.c) return [[EMPTY, 'not in the set', '']]
        const c = d.c
        /** @type {PopRow[]} */
        const rows = [
            [
                uptimeFill(d),
                'signed',
                thousands(c.signed) + ' of ' + thousands(c.in_set),
            ],
            [null, '', shownPct(c.signed, c.in_set, 2).toFixed(2) + ' %'],
            [null, 'absent', thousands(c.absent)],
            [null, 'other block', thousands(c.other_block)],
            [null, 'nil votes', thousands(c.nil) + ' (signed)'],
        ]
        d.outages.forEach(function (o) {
            rows.push([
                null,
                '● outage',
                thousands(o.blocks) +
                    ' blocks, ' +
                    dur(Date.parse(o.end_time) - Date.parse(o.time)) +
                    (o.ongoing ? ', ongoing' : ''),
            ])
            // A day the outage continues into dates its start.
            let start = o.time.slice(11, 16)
            if (o.time.slice(0, 10) !== d.day)
                start = o.time.slice(0, 10) + ' ' + start
            rows.push([
                null,
                '',
                'from ' + start + ' UTC, #' + thousands(o.height),
            ])
        })
        return rows
    }
    /**
     * @param {ChartDay} d
     * @returns {string}
     */
    function proposalsFill(d) {
        if (!d.c || !d.c.proposals_expected) return EMPTY
        return BAND_FILL[
            missedShare(d.c.proposals_missed, d.c.proposals_expected).band
        ]
    }
    /**
     * @param {ChartDay} d
     * @returns {PopRow[]}
     */
    function proposalsRows(d) {
        if (!d.c || !d.c.proposals_expected)
            return [[EMPTY, 'expected', 'none']]
        const c = d.c
        return [
            [
                proposalsFill(d),
                'missed',
                thousands(c.proposals_missed) +
                    ' of ' +
                    thousands(c.proposals_expected),
            ],
            [
                null,
                '',
                missedShare(c.proposals_missed, c.proposals_expected).text,
            ],
        ]
    }

    // A day series: each day's fill and popover rows, and whether a
    // day with an outage gets a dot.
    /** @type {Series} */
    const UPTIME = { fill: uptimeFill, rows: uptimeRows, dots: true }
    /** @type {Series} */
    const PROPOSALS = {
        fill: proposalsFill,
        rows: proposalsRows,
        dots: false,
    }

    // dayCell draws day d's rect (attrs) coloured and explained by
    // series, with an outage dot at (cx, cy) when the series dots.
    /**
     * @param {SVGSVGElement} svg
     * @param {ChartDay} d
     * @param {Series} series
     * @param {Record<string, number>} attrs
     * @param {number} cx
     * @param {number} cy
     */
    function dayCell(svg, d, series, attrs, cx, cy) {
        const r = filled(svgEl('rect', attrs), series.fill(d))
        r.setAttribute('aria-label', d.day)
        popRows(r, d.day, series.rows(d))
        svg.appendChild(r)
        if (series.dots && d.outages.length)
            svg.appendChild(
                filled(
                    svgEl('circle', {
                        cx: cx,
                        cy: cy,
                        r: 2.5,
                        'pointer-events': 'none',
                    }),
                    'var(--fg)'
                )
            )
    }

    // dayBars draws one bar per day of series; geo gives the
    // chart's height, the bars' top (y) and height (h), and the
    // axis's y.
    /**
     * @param {ChartDay[]} days
     * @param {number} px
     * @param {Series} series
     * @param {{ height: number, y: number, h: number, axisY: number }} geo
     * @returns {SVGSVGElement}
     */
    function dayBars(days, px, series, geo) {
        const svg = svgRoot(days.length * px, geo.height)
        days.forEach(function (d, i) {
            dayCell(
                svg,
                d,
                series,
                {
                    x: i * px + 1,
                    y: geo.y,
                    width: px - 2,
                    height: geo.h,
                    rx: 1,
                },
                i * px + px / 2,
                4
            )
        })
        monthAxis(svg, days, px, geo.axisY)
        return svg
    }

    // calendar draws one cell per day, weeks as columns from
    // Monday, opened on the newest week by drawCharts.
    /**
     * @param {ChartDay[]} days
     * @param {Series} series
     * @returns {HTMLDivElement}
     */
    function calendar(days, series) {
        const s = 13
        const step = s + 3
        const first = (new Date(present(days[0]).t).getUTCDay() + 6) % 7
        const weeks = Math.ceil((days.length + first) / 7)
        const height = 16 + 7 * step
        const wrap = mkEl('div', null, 'val-cal')
        const labels = svgRoot(30, height)
        ;['Mon', 'Wed', 'Fri'].forEach(function (l, i) {
            labels.appendChild(axisText(0, 16 + i * 2 * step + s - 3, l))
        })
        wrap.appendChild(labels)
        const svg = svgRoot(weeks * step, height)
        // The first week's month is named unless a month starts
        // in the first two weeks, whose label would collide.
        let startCol = -1
        days.forEach(function (d, i) {
            if (startCol < 0 && d.day.slice(8) === '01')
                startCol = Math.floor((i + first) / 7)
        })
        if (startCol < 0 || startCol >= 2)
            svg.appendChild(axisText(0, 10, month(present(days[0]).day)))
        days.forEach(function (d, i) {
            const col = Math.floor((i + first) / 7)
            const row = (i + first) % 7
            if (d.day.slice(8) === '01')
                svg.appendChild(axisText(col * step, 10, month(d.day)))
            dayCell(
                svg,
                d,
                series,
                {
                    x: col * step,
                    y: 16 + row * step,
                    width: s,
                    height: s,
                    rx: 2,
                },
                col * step + s / 2,
                16 + row * step + s / 2
            )
        })
        const scroll = mkEl('div', null, 'val-cal-scroll')
        scroll.appendChild(svg)
        wrap.appendChild(scroll)
        return wrap
    }

    // recoveryChart draws one bar per sample on its restart's day,
    // its height on a log scale up to 24 h; a blue line over the
    // ones the scoring takes, as the report marks them.
    /**
     * @param {ChartDay[]} days
     * @param {number} px
     * @param {ValidatorReport} rep
     * @returns {SVGSVGElement}
     */
    function recoveryChart(days, px, rep) {
        const base = 94
        const svg = svgRoot(days.length * px, 120)
        /** @type {Record<string, number>} */
        const index = {}
        days.forEach(function (d, i) {
            index[d.day] = i
        })
        /** @type {Record<string, ValidatorRecovery[]>} */
        const byDay = {}
        rep.recoveries.forEach(function (r) {
            if (r.pending) return
            const day = r.restart_time.slice(0, 10)
            if (index[day] === undefined) return
            ;(byDay[day] = byDay[day] || []).push(r)
        })
        /** @type {number | null} */
        let lineFrom = null
        /** @type {number | null} */
        let lineTo = null
        Object.entries(byDay).forEach(function ([day, rs]) {
            const span = Math.max(px - 2, 6)
            const bw = span / rs.length
            const x0 = present(index[day]) * px + px / 2 - span / 2
            rs.forEach(function (r, j) {
                const s = Math.min(r.duration_ms / 1000, 86400)
                const bh = Math.max(
                    (Math.log(1 + s / 60) / Math.log(1 + 1440)) * (base - 24),
                    8
                )
                const x = x0 + j * bw
                const fill = BAND_FILL[recoveryBand(r.duration_ms)]
                const bar = filled(
                    svgEl('rect', {
                        x: x,
                        y: base - bh,
                        width: bw,
                        height: bh,
                        rx: 1,
                        'aria-label':
                            day + ', restart #' + thousands(r.restart_height),
                    }),
                    fill
                )
                popRows(bar, day, [
                    [fill, 'signed again after', dur(r.duration_ms)],
                    [null, 'restart', '#' + thousands(r.restart_height)],
                    [null, 'scored', r.scored ? 'yes (its last 3)' : 'no'],
                ])
                svg.appendChild(bar)
                if (r.scored) {
                    if (lineFrom === null || x < lineFrom) lineFrom = x
                    if (lineTo === null || x + bw > lineTo) lineTo = x + bw
                }
            })
        })
        // A scored sample sets both ends.
        if (lineFrom !== null && lineTo !== null) {
            const line = svgEl('line', {
                class: 'val-scored-line',
                x1: lineFrom,
                x2: lineTo,
                y1: 16,
                y2: 16,
                'stroke-width': 2,
            })
            line.style.stroke = 'var(--blue)'
            svg.appendChild(line)
        }
        monthAxis(svg, days, px, base + 2)
        return svg
    }

    // delayLabels names the vote-delay ranges the edges bound.
    /**
     * @param {number[]} edges
     * @returns {string[]}
     */
    function delayLabels(edges) {
        /**
         * @param {number} ms
         * @returns {string}
         */
        function s(ms) {
            return (ms < 0 ? '−' : '') + Math.abs(ms) / 1000
        }
        const out = ['< ' + s(present(edges[0])) + ' s']
        for (let i = 1; i < edges.length; i++) {
            const prev = present(edges[i - 1])
            const edge = present(edges[i])
            out.push(
                prev === -edge
                    ? '±' + s(edge) + ' s'
                    : s(prev) + ' to ' + s(edge) + ' s'
            )
        }
        out.push('> ' + s(present(edges[edges.length - 1])) + ' s')
        return out
    }
    // share writes n of of as a whole percentage, under 1 % as such.
    /**
     * @param {number} n
     * @param {number} of
     * @returns {string}
     */
    function share(n, of) {
        const p = (n / of) * 100
        if (!n) return '0 %'
        return p < 1 ? '< 1 %' : Math.round(p) + ' %'
    }

    // delayChart draws one bar per day, 100 % stacked by the delay
    // ranges, early at the bottom.
    /**
     * @param {ChartDay[]} days
     * @param {number} px
     * @param {ValidatorReport} rep
     * @returns {SVGSVGElement}
     */
    function delayChart(days, px, rep) {
        const base = 104
        const top = 4
        const svg = svgRoot(days.length * px, 128)
        const labels = delayLabels(rep.vote_delay_edges_ms)
        days.forEach(function (d, i) {
            const c = d.c
            if (!c || !c.votes) return
            const g = svgEl('g', { 'aria-label': d.day })
            let y = base
            c.vote_delay_buckets.forEach(function (n, j) {
                const h = (n / c.votes) * (base - top)
                y -= h
                if (h > 0)
                    g.appendChild(
                        filled(
                            svgEl('rect', {
                                x: i * px + 1,
                                y: y,
                                width: px - 2,
                                height: h,
                            }),
                            present(DELAY_FILLS[j])
                        )
                    )
            })
            /** @type {PopRow[]} */
            const rows = labels.map(function (l, j) {
                return [
                    present(DELAY_FILLS[j]),
                    l,
                    share(present(c.vote_delay_buckets[j]), c.votes),
                ]
            })
            /** @type {PopRow[]} */
            const mean = [[null, 'mean', voteDelayText(c.vote_delay_mean_ms)]]
            popRows(g, d.day, mean.concat(rows.reverse()))
            svg.appendChild(g)
        })
        monthAxis(svg, days, px, base + 2)
        return svg
    }

    // legend says what one bar (or cell) is, then the swatch keys,
    // which wrap as one block.
    /**
     * @param {string} unit
     * @param {Array<[string, string, string?]>} keys
     * @returns {HTMLDivElement}
     */
    function legend(unit, keys) {
        const cap = mkEl('div', null, 'val-cap')
        cap.appendChild(mkEl('span', unit, 'unit'))
        const block = mkEl('span', null, 'keys')
        keys.forEach(function (k) {
            const item = mkEl('span')
            const sw = mkEl('span', null, 'sw' + (k[2] ? ' ' + k[2] : ''))
            sw.style.background = k[0]
            item.appendChild(sw)
            item.appendChild(document.createTextNode(k[1]))
            block.appendChild(item)
        })
        cap.appendChild(block)
        return cap
    }

    /** @type {Record<string, ChartDraw>} */
    const charts = {
        uptime: function (days, px, _rep, cal) {
            return {
                chart: cal
                    ? calendar(days, UPTIME)
                    : dayBars(days, px, UPTIME, {
                          height: 59,
                          y: 9,
                          h: 28,
                          axisY: 41,
                      }),
                legend: legend(
                    cal ? 'one cell per UTC day' : 'one bar per UTC day',
                    [
                        [BAND_FILL.ok, '≥ 99.9 % signed'],
                        [BAND_FILL.warn, '95 to 99.9 %'],
                        [BAND_FILL.err, '≤ 95 %'],
                        [EMPTY, 'not in the set'],
                        ['var(--fg)', 'an outage that day', 'round'],
                    ]
                ),
            }
        },
        proposals: function (days, px, _rep, cal) {
            return {
                chart: cal
                    ? calendar(days, PROPOSALS)
                    : dayBars(days, px, PROPOSALS, {
                          height: 50,
                          y: 0,
                          h: 28,
                          axisY: 32,
                      }),
                legend: legend(
                    cal ? 'one cell per UTC day' : 'one bar per UTC day',
                    [
                        [BAND_FILL.ok, 'none missed'],
                        [BAND_FILL.warn, 'under 5 % missed'],
                        [BAND_FILL.err, '5 % or more'],
                        [EMPTY, 'none expected'],
                    ]
                ),
            }
        },
        recovery: function (days, px, rep) {
            return {
                chart: recoveryChart(days, px, rep),
                legend: legend('one bar per restart, on its day', [
                    [BAND_FILL.ok, '≤ 10 min'],
                    [BAND_FILL.warn, '10 min to 24 h'],
                    [BAND_FILL.err, '≥ 24 h'],
                    ['var(--blue)', 'its last 3, the ones scored', 'line'],
                ]),
            }
        },
        delay: function (days, px, rep) {
            return {
                chart: delayChart(days, px, rep),
                legend: legend(
                    'one bar per UTC day, its precommits by delay from the commit time',
                    delayLabels(rep.vote_delay_edges_ms).map(function (l, j) {
                        return [present(DELAY_FILLS[j]), l]
                    })
                ),
            }
        },
    }

    // ---- Charts from the keyboard
    // A chart's days, or restarts, are one tab stop: ← and → move
    // the focus to the one before or after (in a calendar, ↑ and
    // ↓ do, ← and → move a week), Home and End to the first and
    // the newest, which becomes the stop, scrolled into view.
    // One the keyboard focuses shows its popover; the page's
    // popover code hides it when the focus leaves, and on
    // Escape, the page staying open.
    const CHART_ITEM = '[data-chart] [data-pop-rows]'
    // chartOf is the chart box of item el, a CHART_ITEM.
    /**
     * @param {Element} el
     * @returns {Element}
     */
    function chartOf(el) {
        // CHART_ITEM matches only inside a chart's box.
        return /** @type {Element} */ (el.closest('[data-chart]'))
    }
    // chartItems lists the items of item el's chart.
    /**
     * @param {Element} el
     * @returns {Element[]}
     */
    function chartItems(el) {
        return Array.from(chartOf(el).querySelectorAll('[data-pop-rows]'))
    }
    // chartPlace is where the focus is among the charts' items: its
    // chart's box, which a redraw keeps, and its index there; null
    // when it is elsewhere.
    /** @typedef {{ box: Element, index: number }} ChartPlace */
    /** @returns {ChartPlace | null} */
    function chartPlace() {
        const at = document.activeElement
        if (!(at instanceof Element) || !body.contains(at)) return null
        if (!at.matches(CHART_ITEM)) return null
        return {
            box: chartOf(at),
            index: chartItems(at).indexOf(at),
        }
    }
    // focusChartPlace focuses the item at place in the redrawn
    // charts, which makes it its chart's stop and, when the
    // keyboard had the focus, shows its popover.
    /** @param {ChartPlace | null} place */
    function focusChartPlace(place) {
        if (!place) return
        const items = place.box.querySelectorAll('[data-pop-rows]')
        const item = items[place.index]
        if (item && focusable(item)) item.focus()
    }
    body.addEventListener('keydown', function (e) {
        if (e.isComposing || !(e.target instanceof Element)) return
        if (!e.target.matches(CHART_ITEM)) return
        // An arrow with a modifier is the browser's.
        if (e.altKey || e.ctrlKey || e.metaKey) return
        const items = chartItems(e.target)
        const at = items.indexOf(e.target)
        // A calendar is a grid of weeks: ↑ and ↓ move a day,
        // ← and → a week; across a bar chart, ← and → move a day.
        const calendar = e.target.closest('.val-cal') !== null
        const across = calendar ? 7 : 1
        let to
        if (e.key === 'ArrowLeft') to = at - across
        else if (e.key === 'ArrowRight') to = at + across
        else if (calendar && e.key === 'ArrowUp') to = at - 1
        else if (calendar && e.key === 'ArrowDown') to = at + 1
        else if (e.key === 'Home') to = 0
        else if (e.key === 'End') to = items.length - 1
        else return
        e.preventDefault()
        const item = items[to]
        if (item && focusable(item)) item.focus()
    })
    body.addEventListener('focusin', function (e) {
        if (!(e.target instanceof Element)) return
        if (!e.target.matches(CHART_ITEM)) return
        chartItems(e.target).forEach(function (el) {
            el.setAttribute('tabindex', el === e.target ? '0' : '-1')
        })
        if (e.target.matches(':focus-visible')) showPopover(e.target)
    })

    // drawCharts fills the page's chart containers from chartRep.
    function drawCharts() {
        const rep = chartRep
        if (!rep) return
        chartWidth = window.innerWidth
        // A redraw (a zoom changes the width) keeps the
        // keyboard's place.
        const place = chartPlace()
        const days = chartDays(rep)
        const boxes = body.querySelectorAll('[data-chart]')
        if (!days.length) {
            boxes.forEach(function (box) {
                clearChildren(box)
                box.appendChild(mkEl('div', NO_HISTORY, 'val-msg'))
            })
            return
        }
        const phone = window.innerWidth <= 640
        const px = Math.max(DAY_PX, present(boxes[0]).clientWidth / days.length)
        boxes.forEach(function (box) {
            // The boxes' selector names the attribute.
            const key = /** @type {string} */ (box.getAttribute('data-chart'))
            const cal = phone && (key === 'uptime' || key === 'proposals')
            const c = present(charts[key])(days, px, rep, cal)
            clearChildren(box)
            if (cal) box.appendChild(c.chart)
            else {
                const axis = mkEl('div', null, 'val-axis')
                axis.appendChild(c.chart)
                box.appendChild(axis)
            }
            box.appendChild(c.legend)
            // The chart is one tab stop, on its newest item.
            const items = box.querySelectorAll('[data-pop-rows]')
            items.forEach(function (el, i) {
                el.setAttribute('tabindex', i === items.length - 1 ? '0' : '-1')
            })
        })
        body.querySelectorAll('.val-axis, .val-cal-scroll').forEach(
            function (s) {
                s.scrollLeft = s.scrollWidth
            }
        )
        focusChartPlace(place)
        // A scroll moves the other axes to it once: their own scroll
        // events, which come later, find them at that position.
        const axes = body.querySelectorAll('.val-axis')
        /** @type {number | null} */
        let synced = null
        axes.forEach(function (a) {
            a.addEventListener('scroll', function () {
                const left = a.scrollLeft
                if (left === synced) return
                synced = left
                axes.forEach(function (o) {
                    if (o !== a) o.scrollLeft = left
                })
            })
        })
    }
    // A width change redraws the charts: the day's width follows
    // it, and a phone's calendars come and go at 640 px.
    let redrawTimer = 0
    window.addEventListener('resize', function () {
        clearTimeout(redrawTimer)
        redrawTimer = setTimeout(function () {
            if (chartRep && window.innerWidth !== chartWidth) drawCharts()
        }, 150)
    })

    // ---- Profile markdown
    // markdownNodes renders a profile's markdown as DOM nodes, never
    // as HTML: paragraphs (a single newline is a space), #
    // headings, - and 1. lists, and inline **bold**, *italic* or
    // _italic_, `code`, [text](url) and bare URLs. Only http(s)
    // URLs, the scheme in any case, become links; any other
    // keeps its text. A link's URL keeps balanced parentheses,
    // two deep, as Wikipedia's do; a bare URL ends before its
    // trailing punctuation and the closing parentheses it has
    // more of than opening ones (GitHub's autolink rule). A
    // link's text holds no [, which keeps matching a run of them
    // linear. An _italic_ opens at the start or after a non-word
    // character, so snake_case stays text: mdInline drops a match
    // of it after a word character. MD_INLINE holds no lookbehind
    // for that: Safari before 16.4 cannot parse one.
    const MD_HEADING = /^\s*#{1,6}\s+/
    const MD_BULLET = /^\s*[-*+]\s+/
    const MD_NUMBERED = /^\s*\d+[.)]\s+/
    const MD_INLINE =
        /`([^`]+)`|\[([^[\]]+)\]\(((?:[^()\s]|\((?:[^()\s]|\([^()\s]*\))*\))+)\)|\*\*([^*]+)\*\*|\*([^*\s][^*]*?)\*|_([^_\s][^_]*?)_(?!\w)|(https?:\/\/[^\s<>.,;:!?'"\])][^\s<>]*)/gi
    /**
     * @param {string} md
     * @returns {DocumentFragment}
     */
    function markdownNodes(md) {
        const out = document.createDocumentFragment()
        const lines = md.replace(/\r\n?/g, '\n').split('\n')
        /** @param {string} l */
        const block = function (l) {
            return (
                MD_HEADING.test(l) || MD_BULLET.test(l) || MD_NUMBERED.test(l)
            )
        }
        /** @param {number} i */
        const lineAt = function (i) {
            return present(lines[i])
        }
        for (let i = 0; i < lines.length; ) {
            const line = lineAt(i)
            if (!line.trim()) {
                i++
            } else if (MD_HEADING.test(line)) {
                const text = line
                    .replace(MD_HEADING, '')
                    .replace(/\s+#+\s*$/, '')
                out.appendChild(mdInline(mkEl('div', null, 'val-md-h'), text))
                i++
            } else if (MD_BULLET.test(line) || MD_NUMBERED.test(line)) {
                const item = MD_BULLET.test(line) ? MD_BULLET : MD_NUMBERED
                const list = document.createElement(
                    item === MD_BULLET ? 'ul' : 'ol'
                )
                if (item === MD_NUMBERED) {
                    // It counts from its first item's number.
                    const first = parseInt(line.trim(), 10)
                    if (first !== 1) list.setAttribute('start', String(first))
                }
                for (; i < lines.length && item.test(lineAt(i)); i++)
                    list.appendChild(
                        mdInline(
                            document.createElement('li'),
                            lineAt(i).replace(item, '')
                        )
                    )
                out.appendChild(list)
            } else {
                const para = []
                for (
                    ;
                    i < lines.length && lineAt(i).trim() && !block(lineAt(i));
                    i++
                )
                    para.push(lineAt(i).trim())
                out.appendChild(
                    mdInline(document.createElement('p'), para.join(' '))
                )
            }
        }
        return out
    }
    // bareURLEnd returns where a bare URL ends: before its
    // trailing punctuation and the closing parentheses it has
    // more of than opening ones.
    /**
     * @param {string} url
     * @returns {number}
     */
    function bareURLEnd(url) {
        const opening = url.split('(').length - 1
        let closing = url.split(')').length - 1
        let end = url.length
        for (; end > 0; end--) {
            const c = url.charAt(end - 1)
            if (c === ')' && closing > opening) closing--
            else if (!'.,;:!?\'"]'.includes(c)) break
        }
        return end
    }
    // mdInline appends text's inline markdown to el, as text nodes
    // and elements. It collects every match before rendering any:
    // rendering bold and italic recurses into mdInline, which
    // resets the shared /g regex's lastIndex.
    /**
     * @template {HTMLElement} E
     * @param {E} el
     * @param {string} text
     * @returns {E}
     */
    function mdInline(el, text) {
        let at = 0
        let m
        MD_INLINE.lastIndex = 0
        const spans = []
        while ((m = MD_INLINE.exec(text))) {
            // An _italic_ after a word character is none: the scan
            // goes on from the character after its _.
            if (m[6] !== undefined && /\w/.test(text.charAt(m.index - 1))) {
                MD_INLINE.lastIndex = m.index + 1
                continue
            }
            spans.push(m)
        }
        spans.forEach(function (m) {
            if (m.index > at)
                el.appendChild(document.createTextNode(text.slice(at, m.index)))
            at = m.index + m[0].length
            if (m[1] !== undefined) el.appendChild(mkEl('code', m[1]))
            else if (m[2] !== undefined)
                el.appendChild(
                    /^https?:\/\//i.test(present(m[3]))
                        ? mkLink(m[2], present(m[3]))
                        : document.createTextNode(m[2])
                )
            else if (m[4] !== undefined)
                el.appendChild(mdInline(mkEl('strong'), m[4]))
            else if (m[5] !== undefined || m[6] !== undefined)
                el.appendChild(mdInline(mkEl('em'), present(m[5] ?? m[6])))
            else {
                const bare = present(m[7])
                const url = bare.slice(0, bareURLEnd(bare))
                el.appendChild(mkLink(url, url))
                if (url.length < bare.length)
                    el.appendChild(
                        document.createTextNode(bare.slice(url.length))
                    )
            }
        })
        if (at < text.length)
            el.appendChild(document.createTextNode(text.slice(at)))
        return el
    }

    // ---- Content
    // statusLine says whether the validator is in the set, since
    // when, with its power, and where its node runs.
    /**
     * @param {ValidatorReport} rep
     * @returns {HTMLDivElement}
     */
    function statusLine(rep) {
        const line = mkEl('div', null, 'val-status')
        const st = rep.status
        if (st) {
            let text = st.in_set ? 'in the set' : 'not in the set'
            if (st.since_time) {
                // Days as of the report: the browser's clock may
                // be off.
                const days = Math.floor(
                    (Date.parse(rep.now) - Date.parse(st.since_time)) / 86400000
                )
                // A bound when its join is older than the history.
                const before = st.since_before ? 'before ' : ''
                text +=
                    ' since ' +
                    before +
                    st.since_time.slice(0, 10) +
                    ' (' +
                    (st.since_before ? 'over ' : '') +
                    days +
                    ' d)'
            }
            // Its power, "-" while unknown, and its share of the
            // set's, left out while the set's total is unknown.
            if (st.in_set)
                text +=
                    ' · power ' +
                    (st.power === null ? '-' : thousands(st.power)) +
                    (st.share === null
                        ? ''
                        : ' (' + (st.share * 100).toFixed(1) + ' %)')
            const s = mkEl('span')
            s.appendChild(
                mkEl('span', null, 'dot ' + (st.in_set ? 'green' : 'out'))
            )
            s.appendChild(document.createTextNode(text))
            s.appendChild(
                info(
                    'whether it is in the validator set, since when without a break (key rotations included), its voting power and share of the set',
                    "the node's current validator set; the block history's set changes for since, a bound (since before) when its join is older than the history",
                    ''
                )
            )
            line.appendChild(s)
        }
        const loc = rep.location
        if (loc && (loc.country || loc.provider)) {
            const flag = countryFlag(loc.country)
            const where = mkEl(
                'span',
                (flag ? flag + ' ' : '') +
                    (loc.country ? countryName(loc.country) : '—') +
                    ' · ' +
                    (loc.provider || '—')
            )
            where.appendChild(
                info(
                    'where its node is hosted',
                    "its matched peer's IP, through the GeoIP and ASN databases",
                    ''
                )
            )
            line.appendChild(where)
        }
        return line
    }

    /**
     * @param {ValidatorReport} rep
     * @returns {HTMLDivElement}
     */
    function profileSection(rep) {
        const sec = section(
            'profile',
            'Profile',
            info(
                "what the operator wrote in its valopers profile, and the realm's state for it",
                'the valopers realm (gno.land/r/gnops/valopers), read every 15 min',
                ''
            )
        )
        const kv = mkEl('div', null, 'kv')
        // row adds a key and its value, text or a node, and
        // returns the value's cell.
        /**
         * @param {string} k
         * @param {string | Node} v
         * @param {string} [cls]
         * @returns {HTMLElement}
         */
        function row(k, v, cls) {
            kv.appendChild(mkEl('span', k, 'k'))
            const cell = mkEl(
                typeof v === 'string' ? 'span' : 'div',
                typeof v === 'string' ? v : null,
                'v' + (cls ? ' ' + cls : '')
            )
            if (typeof v !== 'string') cell.appendChild(v)
            kv.appendChild(cell)
            return cell
        }
        const p = rep.profile
        if (p) {
            if (p.description && p.description.trim()) {
                const description = row(
                    'description',
                    markdownNodes(p.description),
                    'val-text'
                )
                // A click copies the markdown as written, not
                // its rendering.
                description.setAttribute('data-copy', p.description)
            } else {
                // A description of blanks is none.
                row('description', '—', 'val-text')
            }
            row('server type', p.server_type || '—')
            row('operator', p.operator)
            row('signing address', p.signing_address)
            row('signing key', p.signing_pubkey)
            if (p.keep_running !== undefined)
                row('keep running', p.keep_running ? 'yes' : 'no')
            if (p.last_rotation_height !== undefined)
                row('last rotation', '#' + thousands(p.last_rotation_height))
        } else {
            const keys = rep.keys || []
            row('operator', rep.operator || '—')
            const lastKey = keys[keys.length - 1]
            row('signing address', lastKey ? lastKey.address : rep.address)
            row('profile', 'none in the valopers realm')
        }
        sec.appendChild(kv)
        return sec
    }

    /**
     * @param {ValidatorReport} rep
     * @returns {HTMLDivElement}
     */
    function scoredGroup(rep) {
        const sm = rep.summary
        const live = rep.live
        const group = mkEl('div', null, 'val-scored')
        const label = mkEl('div', 'scored by the ranking', 'val-glabel')
        label.appendChild(
            info(
                'the sections the validator ranking scores; its score combines uptime (50 %), outages (20 %), recovery (20 %) and proposals (10 %)',
                'the block history',
                'all three, with the weights above'
            )
        )
        group.appendChild(label)

        const up = section(
            'uptime',
            'Uptime',
            info(
                'per UTC day, of the blocks it was in the set for: signed (a nil vote counts as signed), absent from the commit, or voted for another block',
                "the block history: every block's commit",
                'signed ÷ blocks in the set (50 %), and outages, runs of 10 or more absences (20 %), over the 30 complete UTC days before today; outages leave recovery runs out, as the ranking does; signed counts every height, where the ranking leaves recovery runs out'
            )
        )
        const signed = sm.in_set ? shownPct(sm.signed, sm.in_set, 2) : null
        up.appendChild(
            statsLine([
                [
                    'last 30 days',
                    [
                        [
                            'signed',
                            signed === null ? '—' : signed.toFixed(2) + ' %',
                            signed === null ? '' : scoreBand.uptime(signed),
                        ],
                        [
                            'outages',
                            thousands(sm.outages),
                            sm.in_set ? scoreBand.outages(sm.outages) : '',
                        ],
                        ['nil votes', thousands(sm.nil), ''],
                        ['other block', thousands(sm.other_block), ''],
                    ],
                ],
            ])
        )
        up.appendChild(chart('uptime'))
        group.appendChild(up)

        const pr = section(
            'proposals',
            'Proposals',
            info(
                'per UTC day: the rounds it was the expected proposer of until the block was made, and those it missed',
                "the block history: block headers and each round's expected proposer",
                'missed ÷ expected (10 %) over the 30 complete UTC days before today; the ranking counts only heights it signed and leaves restart heights out, the figures here count every height'
            )
        )
        const pm = sm.proposals_missed
        const pe = sm.proposals_expected
        const missed = pe ? missedShare(pm, pe) : null
        pr.appendChild(
            statsLine([
                [
                    'last 30 days',
                    [
                        [
                            'missed',
                            missed
                                ? thousands(pm) +
                                  ' of ' +
                                  thousands(pe) +
                                  ' (' +
                                  missed.text +
                                  ')'
                                : 'none expected',
                            missed ? missed.band : '',
                        ],
                    ],
                ],
                [
                    'last 24 h',
                    [
                        [
                            'missed',
                            live.expected_proposals_24h
                                ? thousands(live.missed_proposals_24h) +
                                  ' of ' +
                                  thousands(live.expected_proposals_24h)
                                : 'none expected',
                            '',
                        ],
                    ],
                ],
            ])
        )
        pr.appendChild(chart('proposals'))
        group.appendChild(pr)

        const rc = sm.recovery
        const re = section(
            'recovery',
            'Recovery',
            info(
                'after a restart (two blocks more than 2 min apart), how long it took to sign again; it gets a sample only if it was signing before the restart',
                'the block history: restart gaps and absences',
                'the median of its last 3 samples within the 180 complete UTC days before today (20 %)'
            )
        )
        re.appendChild(
            statsLine([
                [
                    'last 180 days',
                    [
                        [
                            'median of its last 3',
                            rc.samples ? dur(rc.median_ms) : '—',
                            rc.samples ? recoveryBand(rc.median_ms) : '',
                        ],
                        ['samples', thousands(rc.samples), ''],
                        ['slowest', rc.samples ? dur(rc.slowest_ms) : '—', ''],
                    ],
                ],
            ])
        )
        re.appendChild(chart('recovery'))
        group.appendChild(re)
        return group
    }

    /**
     * @param {ValidatorReport} rep
     * @returns {HTMLDivElement}
     */
    function delaySection(rep) {
        const sec = section(
            'delay',
            'Vote delay',
            info(
                "how far its precommits' times are from the commit's voting-power-weighted median time; negative is early",
                'the block history: daily vote-delay histograms; the live window for the last 100 blocks',
                ''
            )
        )
        const live = rep.live.vote_delay_ms
        sec.appendChild(
            statsLine([
                [
                    'last 100 blocks',
                    [['median', live === null ? '—' : voteDelayText(live), '']],
                ],
                [
                    'last 30 days',
                    [
                        [
                            'mean',
                            rep.summary.votes
                                ? voteDelayText(rep.summary.vote_delay_mean_ms)
                                : '—',
                            '',
                        ],
                    ],
                ],
            ])
        )
        sec.appendChild(chart('delay'))
        return sec
    }

    // timelineRow builds a Timeline entry in the Timeline view's
    // row style, its age as of now (ms since the epoch); null for a
    // kind the page does not know.
    /**
     * @param {TimelineEntry} e
     * @param {number} now
     * @returns {HTMLDivElement | null}
     */
    function timelineRow(e, now) {
        let k, cls, what, p
        switch (e.kind) {
            case 'recovering':
                k = '↻'
                cls = 'vt-out'
                what = ['not signed since the restart']
                p = 'restart at #' + thousands(e.restart_height)
                break
            case 'recovered':
                k = '↻'
                cls = 'vt-rec'
                what = [
                    'recovered in ',
                    [dur(e.duration_ms), recoveryBand(e.duration_ms)],
                ]
                p = 'restart at #' + thousands(e.restart_height)
                break
            case 'outage':
                k = '■'
                cls = 'vt-out'
                what = ['outage of ' + thousands(e.blocks) + ' blocks']
                p =
                    dur(Date.parse(e.end_time) - Date.parse(e.time)) +
                    (e.ongoing ? ', ongoing' : '')
                break
            case 'power':
                k = '↕'
                cls = 'vt-pow'
                what = [
                    'power ' + thousands(e.before) + ' → ' + thousands(e.after),
                ]
                p = ''
                break
            case 'rotated':
                k = '⇄'
                cls = 'vt-rot'
                what = ['rotated its key']
                p = shortAddr(e.old_address) + ' → ' + shortAddr(e.new_address)
                break
            case 'joined':
            case 'left':
            case 'genesis':
                k = { joined: '+', left: '−', genesis: '◆' }[e.kind]
                cls = {
                    joined: 'vt-add',
                    left: 'vt-rm',
                    genesis: 'vt-gen',
                }[e.kind]
                what = [e.kind === 'genesis' ? 'in the genesis set' : e.kind]
                p = 'power ' + thousands(e.power)
                break
            default:
                return null
        }
        const r = mkEl('div', null, 'vt-row')
        const h = mkEl(
            'span',
            '#' + thousands(e.height) + ' · ' + timeAgo(e.time, now),
            'h tap-popover',
            { 'data-popover': utcMinute(e.time) }
        )
        r.appendChild(h)
        r.appendChild(mkEl('span', k, 'k ' + cls))
        const w = mkEl('span', null, 'w')
        what.forEach(function (seg) {
            w.appendChild(
                typeof seg === 'string'
                    ? document.createTextNode(seg)
                    : mkEl('b', seg[0], seg[1])
            )
        })
        if (p) {
            w.appendChild(document.createTextNode(' '))
            w.appendChild(mkEl('span', p, 'p'))
        }
        r.appendChild(w)
        r.appendChild(mkEl('span', '', 'n'))
        return r
    }

    /**
     * @param {ValidatorReport} rep
     * @returns {HTMLDivElement}
     */
    function timelineSection(rep) {
        const sec = section(
            'timeline',
            'Timeline',
            info(
                'its changes: joins, leaves, power, key rotations, outages, recoveries after a restart',
                'the block history: validator sets, set-change events, absences, restart gaps',
                'its outages and recoveries (see Uptime and Recovery); the rest is not'
            )
        )
        // Ages as of the report: the browser's clock may be off.
        const now = Date.parse(rep.now)
        const rows = rep.timeline
            .map(function (e) {
                return timelineRow(e, now)
            })
            .filter(function (r) {
                return r !== null
            })
        if (!rows.length) sec.appendChild(mkEl('div', NO_HISTORY, 'val-msg'))
        rows.forEach(function (r) {
            sec.appendChild(r)
        })
        return sec
    }

    // render shows report rep.
    /** @param {ValidatorReport} rep */
    function render(rep) {
        // Without a moniker, the operator the report resolved
        // to names it, as the URL does.
        title.textContent =
            rep.moniker || shortAddr(rep.operator || rep.address)
        clearChildren(body)
        body.appendChild(statusLine(rep))
        body.appendChild(profileSection(rep))
        body.appendChild(scoredGroup(rep))
        body.appendChild(delaySection(rep))
        body.appendChild(timelineSection(rep))
        chartRep = rep
        drawCharts()
    }
    // message shows text in place of a report.
    /** @param {string} text */
    function message(text) {
        clearChildren(body)
        body.appendChild(mkEl('div', text, 'val-msg'))
    }

    // ---- Opening and closing
    // urlWith is the page's URL with ?val= set to val, or without it
    // for null.
    /**
     * @param {string | null} val
     * @returns {string}
     */
    function urlWith(val) {
        const p = new URLSearchParams(location.search)
        if (val) p.set('val', val)
        else p.delete('val')
        const s = p.toString()
        return location.pathname + (s ? '?' + s : '') + location.hash
    }
    // show opens the modal on val and reads its report; the URL
    // follows to the report's operator when it names one. from
    // is the name clicked to open it, if any: a browser that
    // does not focus a link on a click leaves it unfocused.
    /**
     * @param {string} val
     * @param {Element} [from]
     */
    function show(val, from) {
        // A popover open on the page, or on the validator shown
        // before, would stay over the dialog.
        hidePopover()
        shown = val
        chartRep = null
        if (!modal.classList.contains('open')) {
            opener = from || document.activeElement
            openerAt = shownLinksLike(opener).findIndex((a) => a === opener)
            modal.classList.add('open')
        }
        title.textContent = shortAddr(val)
        message('loading…')
        $('val-close').focus()
        const s = ++seq
        boundedRead(REPORT_READ_BOUND_MS, function (signal) {
            return fetch('/api/validator?val=' + encodeURIComponent(val), {
                signal,
            }).then(
                /**
                 * The report, its JSON unchecked, or what failed.
                 * @param {Response} res
                 * @returns {Promise<{ rep: ValidatorReport } | { error: string }>}
                 */
                function (res) {
                    if (res.ok)
                        return res.json().then(
                            /** @param {ValidatorReport} rep */
                            function (rep) {
                                return { rep: rep }
                            }
                        )
                    // An error answer names its error in JSON; any
                    // other, a proxy's error page, says its status.
                    const failed = 'could not load: HTTP ' + res.status
                    return res.json().then(
                        /** @param {unknown} d */
                        function (d) {
                            // The JSON is unchecked: an error that is
                            // no text, which gnockpit never sends,
                            // says the status too.
                            return { error: errorOf(d) ?? failed }
                        },
                        function () {
                            return { error: failed }
                        }
                    )
                }
            )
        }).then(
            function (r) {
                if (s !== seq) return
                if ('error' in r) {
                    message(r.error)
                    return
                }
                try {
                    render(r.rep)
                } catch (e) {
                    // The page's fault, not the read's: said
                    // so, and thrown on for the console.
                    chartRep = null
                    message('could not show it: ' + failureMessage(e))
                    setTimeout(function () {
                        throw e
                    })
                    return
                }
                if (r.rep.operator && r.rep.operator !== val) {
                    shown = r.rep.operator
                    history.replaceState(
                        history.state ? { val: shown } : null,
                        '',
                        urlWith(shown)
                    )
                }
            },
            /** @param {unknown} e */
            function (e) {
                if (s === seq) message('could not load: ' + failureMessage(e))
            }
        )
    }
    function hide() {
        shown = null
        chartRep = null
        seq++
        modal.classList.remove('open')
        hidePopover()
        focusBack(opener)
        opener = null
    }
    // shownLinksLike returns the links to el's validator the page
    // shows, in document order; none when el is no such link.
    /**
     * @param {Element | null} el
     * @returns {HTMLAnchorElement[]}
     */
    function shownLinksLike(el) {
        if (!el || !el.matches('a.val-link')) return []
        const href = el.getAttribute('href')
        const links = /** @type {NodeListOf<HTMLAnchorElement>} */ (
            document.querySelectorAll('a.val-link')
        )
        return Array.from(links).filter(function (a) {
            return (
                a.getAttribute('href') === href && a.getClientRects().length > 0
            )
        })
    }
    // focusBack gives the focus back to el, which opened the
    // page. When the dashboard re-rendered or hid el meanwhile
    // (a refresh, the table and cards swapping at 768 px), the
    // shown link to the same validator at el's place among them
    // takes it, else the first.
    /** @param {Element | null} el */
    function focusBack(el) {
        if (!el) return
        const visible = el.isConnected && el.getClientRects().length > 0
        if (visible || !el.matches('a.val-link')) {
            if (focusable(el)) el.focus()
            return
        }
        const links = shownLinksLike(el)
        const same = links[openerAt] || links[0]
        if (same) same.focus()
    }
    // openValidator opens val's page from link from, a history entry
    // of its own unless the URL names val.
    /**
     * @param {string} val
     * @param {Element} from
     */
    function openValidator(val, from) {
        if (new URLSearchParams(location.search).get('val') !== val) {
            history.pushState({ val: val }, '', urlWith(val))
            pushed = true
        }
        show(val, from)
    }
    function closeValidator() {
        if (!shown) return
        hide()
        if (pushed) {
            pushed = false
            history.back()
        } else history.replaceState(null, '', urlWith(null))
    }
    window.addEventListener('popstate', function () {
        const val = new URLSearchParams(location.search).get('val')
        if (val) {
            pushed = pushedEntry()
            if (val !== shown) show(val)
        } else if (shown) {
            pushed = false
            hide()
        }
    })
    $('val-close').addEventListener('click', closeValidator)
    modal.addEventListener('click', function (e) {
        if (e.target === modal) closeValidator()
    })
    document.addEventListener('keydown', function (e) {
        if (!shown) return
        if (e.key === 'Escape') closeValidator()
        if (e.key === 'Tab') keepFocusIn(modal, e)
    })
    // The link names the validator alone, without the page's
    // other parameters (an ETA card's).
    $('val-link-btn').addEventListener('click', function (e) {
        // The button is the open page's, which shows a validator.
        const val = /** @type {string} */ (shown)
        copyNotify(
            location.origin +
                location.pathname +
                '?val=' +
                encodeURIComponent(val),
            e
        )
    })
    // A plain click on a validator's name opens its page, and no
    // other handler sees it (the row's or the card's copy); a
    // modified click follows the link.
    document.addEventListener(
        'click',
        function (e) {
            if (!(e.target instanceof Element)) return
            const a = /** @type {HTMLAnchorElement | null} */ (
                e.target.closest('a.val-link')
            )
            if (!a) return
            if (
                e.button !== 0 ||
                e.metaKey ||
                e.ctrlKey ||
                e.shiftKey ||
                e.altKey
            )
                return
            e.preventDefault()
            e.stopPropagation()
            // valLink builds the link with val.
            const val = /** @type {string} */ (
                new URL(a.href).searchParams.get('val')
            )
            openValidator(val, a)
        },
        true
    )
    // A reload keeps the entry an open pushed, so closing goes back.
    const initial = new URLSearchParams(location.search).get('val')
    if (initial) {
        pushed = pushedEntry()
        show(initial)
    }
}
