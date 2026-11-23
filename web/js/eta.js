// Countdown cards and the planner. The cards poll /api/eta; between
// polls the countdown ticks down locally from the server's
// seconds_left, so the viewer's clock does not matter.

/** @import { EtaBlock, EtaErrorBody, EtaHalt, EtaHeightEstimate, EtaHistory, EtaPace, EtaReport, EtaScenario, EtaTarget, EtaTimeRange } from '../types/eta.js' */
/** @import { DeviceIDAnswer, ReminderRemovalAnswer, ReminderView } from '../types/push.js' */

import {
    answerTimeoutMs,
    browserPushSub,
    deviceKeys,
    keepDevice,
    saveFailure,
    whenPushReady,
} from './alerts.js'
import { copyNotify } from './copy.js'
import { $, $in } from './dom.js'
import { keepFocusIn } from './focus.js'
import { MONTHS, thousands, twoDigits } from './format.js'
import { present } from './present.js'
import { boundedRead } from './read.js'
import { live } from './state.js'
import { storageGet, storageSet } from './storage.js'

/**
 * An /api/eta answer as getETA gives it: the report, which names no
 * error, or the error that kept it, the server's or the read's.
 * @typedef {(EtaReport & { error?: undefined }) | EtaErrorBody} EtaAnswer
 */
/**
 * The report of a height, as fillETAHeight fills it (web/eta.go): its
 * target, its ETA and the seconds left to it, the newest block and the
 * history, and its scenarios' ETAs. Reached, it has its time; not yet,
 * its pace, and its ranges once the history allows.
 * @typedef {Omit<EtaReport, 'scenarios'> & {
 *     error?: undefined,
 *     now: EtaBlock,
 *     history: EtaHistory,
 *     target: EtaTarget,
 *     eta: string,
 *     seconds_left: number,
 *     scenarios?: Array<EtaScenario & { eta: string }>,
 * }} HeightReport
 */
/**
 * An answer a countdown card shows: a height's report, or its error.
 * @typedef {HeightReport | EtaErrorBody} HeightAnswer
 */

export function start() {
    const ETA_POLL_MS = 10000
    // ETA_READ_BOUND_MS bounds a read of /api/eta: one without an
    // answer by then is given up, and the card says so.
    const ETA_READ_BOUND_MS = 10000
    const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
    // HISTORY_SHORT is why a range is pending.
    const HISTORY_SHORT = 'the block-time history is still too short'
    /** @type {Record<string, string>} */
    const PACE_OF = {
        last_hour: 'last hour',
        last_24h: 'last 24 h',
        last_7d: 'last 7 days',
        last_30d: 'last 30 days',
        all_history: 'all history',
    }

    // esc escapes s for the markup the cards build.
    /**
     * @param {string} s
     * @returns {string}
     */
    function esc(s) {
        return String(s).replace(/[&<>"]/g, function (c) {
            return present(
                /** @type {Record<string, string>} */ ({
                    '&': '&amp;',
                    '<': '&lt;',
                    '>': '&gt;',
                    '"': '&quot;',
                })[c]
            )
        })
    }
    // ms is RFC 3339 time iso in ms since the epoch.
    /**
     * @param {string} iso
     * @returns {number}
     */
    function ms(iso) {
        return new Date(iso).getTime()
    }
    // hms, hm and day write t, in ms since the epoch, in UTC: its
    // time to the second, to the minute, and its day.
    /**
     * @param {number} t
     * @returns {string}
     */
    function hms(t) {
        const d = new Date(t)
        return (
            twoDigits(d.getUTCHours()) +
            ':' +
            twoDigits(d.getUTCMinutes()) +
            ':' +
            twoDigits(d.getUTCSeconds())
        )
    }
    /**
     * @param {number} t
     * @returns {string}
     */
    function hm(t) {
        return hms(t).slice(0, 5)
    }
    /**
     * @param {number} t
     * @returns {string}
     */
    function day(t) {
        const d = new Date(t)
        return (
            DAYS[d.getUTCDay()] +
            ' ' +
            d.getUTCDate() +
            ' ' +
            MONTHS[d.getUTCMonth()]
        )
    }
    // local is t on the viewer's clock, with its day when that
    // differs from the UTC one, or "" for a viewer on UTC.
    /**
     * @param {number} t
     * @returns {string}
     */
    function local(t) {
        const d = new Date(t)
        if (d.getTimezoneOffset() === 0) return ''
        return (
            twoDigits(d.getHours()) +
            ':' +
            twoDigits(d.getMinutes()) +
            (d.getDay() !== d.getUTCDay() ? ' ' + DAYS[d.getDay()] : '') +
            ' your time'
        )
    }
    // onDay is time, the clock of t, after t's day when that
    // is not today in UTC on the server's clock.
    /**
     * @param {number} t
     * @param {string} time
     * @returns {string}
     */
    function onDay(t, time) {
        const today = utcDay(Date.now() + live.serverTimeOffset)
        return utcDay(t) === today ? time : day(t) + ' ' + time
    }
    // utcDay numbers the UTC day of t, a time in milliseconds.
    /**
     * @param {number} t
     * @returns {number}
     */
    function utcDay(t) {
        return Math.floor(t / 864e5)
    }
    // countdown writes sec seconds as a countdown: [Nd ]HH:MM:SS.
    /**
     * @param {number} sec
     * @returns {string}
     */
    function countdown(sec) {
        const s = Math.max(0, Math.round(sec))
        const d = Math.floor(s / 86400)
        const h = Math.floor((s % 86400) / 3600)
        const m = Math.floor((s % 3600) / 60)
        return (
            (d ? d + 'd ' : '') +
            twoDigits(h) +
            ':' +
            twoDigits(m) +
            ':' +
            twoDigits(s % 60)
        )
    }
    // inShort writes sec seconds in their two largest units, to the
    // minute.
    /**
     * @param {number} sec
     * @returns {string}
     */
    function inShort(sec) {
        const m = Math.round(sec / 60)
        const d = Math.floor(m / 1440)
        const h = Math.floor((m % 1440) / 60)
        if (d) return d + 'd ' + h + 'h'
        return h ? h + 'h ' + (m % 60) + 'm' : m + 'm'
    }
    // drift writes sec seconds signed, later positive.
    /**
     * @param {number} sec
     * @returns {string}
     */
    function drift(sec) {
        const s = Math.round(sec)
        const a = Math.abs(s)
        const sign = s > 0 ? '+' : s < 0 ? '−' : '±'
        if (a < 60) return sign + a + 's'
        if (a < 3600)
            return sign + Math.floor(a / 60) + 'm ' + twoDigits(a % 60) + 's'
        return (
            sign +
            Math.floor(a / 3600) +
            'h ' +
            twoDigits(Math.floor((a % 3600) / 60)) +
            'm'
        )
    }
    // rangeText writes time range r to the minute.
    /**
     * @param {EtaTimeRange} r
     * @returns {string}
     */
    function rangeText(r) {
        return hm(ms(r.from)) + '–' + hm(ms(r.to))
    }
    // rangeDays is rangeText with the days, when the range spans
    // more than one.
    /**
     * @param {EtaTimeRange} r
     * @returns {string}
     */
    function rangeDays(r) {
        const a = ms(r.from)
        const b = ms(r.to)
        if (day(a) === day(b)) return rangeText(r)
        return day(a) + ' ' + hm(a) + '–' + day(b) + ' ' + hm(b)
    }
    // row is a card's key and value markup: v is markup.
    /**
     * @param {string} k
     * @param {string} v
     * @returns {string}
     */
    function row(k, v) {
        return (
            '<span class="k">' + k + '</span><span class="v">' + v + '</span>'
        )
    }

    // getETA queries /api/eta; a failure to reach or read it comes
    // back as an answer with an error, like the server's own, and
    // so does a read without an answer in ETA_READ_BOUND_MS, so
    // the polls go on.
    /**
     * @param {string} query
     * @returns {Promise<EtaAnswer>}
     */
    async function getETA(query) {
        const url = '/api/eta' + (query ? '?' + query : '')
        try {
            return await boundedRead(
                ETA_READ_BOUND_MS,
                async function (signal) {
                    const r = await fetch(url, { signal })
                    /** @type {EtaAnswer} */
                    let rep
                    try {
                        rep = await r.json()
                    } catch (e) {
                        // The bound's abort: boundedRead names it.
                        if (signal.aborted) throw e
                        return {
                            error: 'unreadable answer (HTTP ' + r.status + ')',
                        }
                    }
                    if (!r.ok && !rep.error) {
                        // A refused question's answer is an error
                        // answer; another server's may name no error.
                        const failure = /** @type {EtaErrorBody} */ (rep)
                        failure.error = 'HTTP ' + r.status
                    }
                    return rep
                }
            )
        } catch (e) {
            if (e instanceof DOMException && e.name === 'TimeoutError')
                return { error: e.message }
            return { error: 'gnockpit is unreachable' }
        }
    }

    // failed tells an answer that holds no report: getETA gives
    // each its error.
    /**
     * @param {EtaAnswer | HeightAnswer} r
     * @returns {r is EtaErrorBody}
     */
    function failed(r) {
        return !!r.error
    }

    const CARD_BODY =
        '<div class="eta-grid"><div>' +
        '<div class="eta-count">--:--:--</div>' +
        '<div class="eta-target"></div>' +
        '<div class="kv"></div>' +
        '</div><div class="eta-chart">' +
        '<div class="eta-chart-title">ETA over time · cone = 80% range</div>' +
        '<svg></svg>' +
        '</div></div>' +
        '<div class="eta-scen-title">If the chain keeps the pace of…</div>' +
        '<div class="eta-scen"></div>'
    // The compact card shows the range bar under its one line.
    const CARD_BAR =
        '<div class="eta-range"></div>' +
        '<div class="eta-legend">' +
        '<span><i style="background: rgba(88, 166, 255, 0.55)"></i>80% likely</span>' +
        '<span><i style="background: rgba(88, 166, 255, 0.22)"></i>95%</span>' +
        '<span style="color: var(--blue)">● ETA</span>' +
        '<span class="orig" style="color: var(--yellow)">◆ original</span>' +
        '</div>'

    // etaCard drives one countdown card: kind "halt" for the halt
    // scheduled on chain, "link" for the target in the page link.
    /**
     * A countdown card: show shows an answer, hide hides the card,
     * tick counts down, counting tells whether it does, redraw draws
     * its chart and bar again.
     * @typedef {object} EtaCard
     * @property {(r: HeightAnswer, l?: string) => void} show
     * @property {() => void} hide
     * @property {() => void} tick
     * @property {() => boolean} counting
     * @property {() => void} redraw
     */
    /**
     * @param {string} id
     * @param {'halt' | 'link'} kind
     * @returns {EtaCard}
     */
    function etaCard(id, kind) {
        const el = $(id)
        $in(el, '.section-body').innerHTML = CARD_BODY
        $in(el, '.eta-bar').innerHTML = CARD_BAR
        // q is the card's element sel matches, from index.html or
        // from CARD_BODY and CARD_BAR.
        /**
         * @param {string} sel
         * @returns {HTMLElement}
         */
        function q(sel) {
            return $in(el, sel)
        }
        // rep is the answer the card shows, getETA's, its JSON
        // unchecked; null before the first.
        /** @type {HeightAnswer | null} */
        let rep = null
        let fetchedAt = 0
        let label = ''
        // stale is the error of the latest poll while an earlier
        // answer stays on screen.
        let stale = ''
        // halt is the halt the latest answer names: a halt card
        // names it even while an earlier answer stays.
        /** @type {EtaHalt | null} */
        let halt = null

        const cb = /** @type {HTMLInputElement} */ (q('.eta-compact-cb'))
        const compactKey = 'eta-compact-' + kind
        /** @param {boolean} on */
        function setCompact(on) {
            el.classList.toggle('compact', on)
            cb.checked = on
            storageSet(compactKey, on ? '1' : '0')
            // The bar and the chart measure their width: draw
            // whichever this shows.
            if (on) renderRange()
            else drawChart()
        }
        setCompact(storageGet(compactKey) === '1')
        cb.addEventListener('change', function () {
            setCompact(cb.checked)
        })
        // A collapsed card skipped its chart and bar: draw them on
        // reopening.
        q('h2').addEventListener('click', function () {
            setTimeout(function () {
                drawChart()
                renderRange()
            })
        })

        // head names the target of report r, as text:
        // "#1,250,000 · <label>", or "halt #1,250,000 · min v1.8.0".
        /**
         * @param {HeightReport} r
         * @returns {string}
         */
        function head(r) {
            if (kind !== 'halt')
                return (
                    '#' +
                    thousands(r.target.height) +
                    (label ? ' · ' + label : '')
                )
            return haltHead()
        }
        // haltHead names the halt card's halt, by its own height: an
        // answer without an estimate names it too.
        /** @returns {string} */
        function haltHead() {
            // The halt card shows only answers naming a halt (pollETA).
            const h = /** @type {EtaHalt} */ (halt)
            const v = h.min_version
            return 'halt #' + thousands(h.height) + (v ? ' · min ' + v : '')
        }

        // show shows answer r, the target labelled l. An error after
        // a report leaves the report, marked stale.
        /**
         * @param {HeightAnswer} r
         * @param {string} [l]
         */
        function show(r, l) {
            label = l || ''
            halt = r.halt || null
            el.style.display = ''
            if (failed(r) && rep && !failed(rep)) {
                stale = r.error
            } else {
                rep = r
                fetchedAt = Date.now()
                stale = ''
            }
            render(rep)
            tick()
        }
        function hide() {
            el.style.display = 'none'
            rep = null
            stale = ''
            halt = null
        }

        // render draws answer r on the card.
        /** @param {HeightAnswer} r */
        function render(r) {
            q('.kv').innerHTML = ''
            q('.eta-scen').innerHTML = ''
            q('.eta-range').innerHTML = ''
            // A card without any answer yet shows only the error;
            // a produced target has no pace scenarios.
            el.classList.toggle('eta-failed', failed(r))
            el.classList.toggle('eta-reached', !failed(r) && !!r.reached_at)
            el.classList.toggle('eta-norange', failed(r) || !r.range_80)
            if (failed(r)) {
                q('.eta-count').textContent = '–'
                // A halt card names its halt even without an
                // estimate.
                const named = kind === 'halt' ? haltHead() : ''
                q('.eta-target').textContent = named
                q('.kv').innerHTML = row('error', esc(r.error))
                q('.eta-inline').textContent = named
                    ? named + ' · ' + r.error
                    : r.error
                return
            }
            const o = r.original
            let rows = ''
            if (r.reached_at) {
                rows += row('reached', when(ms(r.reached_at)))
            } else {
                rows +=
                    row('eta', when(ms(r.eta))) +
                    row(
                        'likely',
                        r.range_80 && r.range_95
                            ? '<b>' +
                                  rangeDays(r.range_80) +
                                  ' UTC</b> <span class="dim">(80%) · ' +
                                  rangeDays(r.range_95) +
                                  ' (95%)</span>'
                            : '<span class="dim">pending: ' +
                                  HISTORY_SHORT +
                                  '</span>'
                    )
            }
            if (o) {
                const d = o.drift_seconds
                rows +=
                    row(
                        'original',
                        '<b>' +
                            day(ms(o.eta)) +
                            ' ' +
                            hms(ms(o.eta)) +
                            ' UTC</b> <span class="dim">(' +
                            (kind === 'halt' ? 'first seen ' : 'estimated ') +
                            day(ms(o.as_of)) +
                            ' ' +
                            hm(ms(o.as_of)) +
                            ')</span>'
                    ) +
                    row(
                        'drift',
                        Math.abs(d) < 60
                            ? '<b>on time</b>'
                            : '<b>' +
                                  inShort(Math.abs(d)) +
                                  (d < 0 ? ' earlier' : ' later') +
                                  '</b> <span class="dim">than the original</span>'
                    )
            }
            if (!r.reached_at) {
                // Not reached, a height has its pace (fillETAHeight).
                const pace = /** @type {EtaPace} */ (r.pace)
                rows += row(
                    'pace',
                    '<b>' +
                        pace.seconds_per_block.toFixed(3) +
                        ' s/block</b> <span class="dim">(last ' +
                        thousands(pace.over_blocks) +
                        ' blocks)</span>'
                )
                if (!r.history.complete)
                    rows += row(
                        'history',
                        '<span class="dim">still loading' +
                            (r.history.from
                                ? ', back to ' + day(ms(r.history.from))
                                : '') +
                            '</span>'
                    )
                renderRange()
                renderScenarios(r, pace)
            }
            const warn = warning(r)
            if (warn)
                rows += row(
                    'warning',
                    '<span class="warn">' + esc(warn) + '</span>'
                )
            q('.kv').innerHTML = rows
            drawChart()
        }

        // warning is what the card warns of: a halt before the
        // target, and the error of the latest poll while an
        // earlier answer stays, along with report r.
        /**
         * @param {HeightReport} r
         * @returns {string}
         */
        function warning(r) {
            return [r.warning, stale && stale + '; showing the last answer']
                .filter(Boolean)
                .join(' · ')
        }

        // when is a time in UTC, and on the viewer's clock.
        /**
         * @param {number} t
         * @returns {string}
         */
        function when(t) {
            const l = local(t)
            return (
                '<b>' +
                day(t) +
                ' ' +
                hms(t) +
                ' UTC</b>' +
                (l ? ' <span class="dim">(' + l + ')</span>' : '')
            )
        }

        // renderRange draws the 95% and 80% ranges with the
        // current ETA (dot) and the original one (diamond). A
        // range end too close to the ETA for both labels to fit
        // goes unlabelled.
        function renderRange() {
            if (!rep || failed(rep) || rep.reached_at) return
            if (!rep.range_80 || !rep.range_95) return
            q('.eta-legend .orig').style.display = rep.original ? '' : 'none'
            const eta = ms(rep.eta)
            /** @type {[number, number]} */
            const r80 = [ms(rep.range_80.from), ms(rep.range_80.to)]
            /** @type {[number, number]} */
            const r95 = [ms(rep.range_95.from), ms(rep.range_95.to)]
            const orig = rep.original ? ms(rep.original.eta) : eta
            const lo = Math.min(r95[0], orig, eta)
            const hi = Math.max(r95[1], orig, eta)
            const span = Math.max(hi - lo, 60000)
            const a = lo - span * 0.08
            const b = hi + span * 0.08
            const px = q('.eta-range').clientWidth
            // x places v on the bar, w sizes v0 to v1 on it, in % of
            // its width.
            /**
             * @param {number} v
             * @returns {string}
             */
            function x(v) {
                return (((v - a) / (b - a)) * 100).toFixed(2) + '%'
            }
            /**
             * @param {number} v0
             * @param {number} v1
             * @returns {string}
             */
            function w(v0, v1) {
                return (((v1 - v0) / (b - a)) * 100).toFixed(2) + '%'
            }
            // An HH:MM label beside the HH:MM:SS one needs about
            // 44 px between their centres at the labels' size; the
            // ETA's own label (end false) always shows.
            /**
             * @param {number} v
             * @param {string} text
             * @param {boolean} end
             * @returns {string}
             */
            function lbl(v, text, end) {
                if (end && (Math.abs(v - eta) / (b - a)) * px < 44) return ''
                return (
                    '<div class="lbl" style="left:' +
                    x(v) +
                    '">' +
                    text +
                    '</div>'
                )
            }
            q('.eta-range').innerHTML =
                '<div class="track"></div>' +
                '<div class="b95" style="left:' +
                x(r95[0]) +
                ';width:' +
                w(r95[0], r95[1]) +
                '"></div>' +
                '<div class="b80" style="left:' +
                x(r80[0]) +
                ';width:' +
                w(r80[0], r80[1]) +
                '"></div>' +
                (rep.original
                    ? '<div class="orig" style="left:' +
                      x(orig) +
                      '" data-popover="original ETA ' +
                      hms(orig) +
                      ' UTC"></div>'
                    : '') +
                '<div class="now" style="left:' +
                x(eta) +
                '" data-popover="ETA ' +
                hms(eta) +
                ' UTC"></div>' +
                lbl(r95[0], hm(r95[0]), true) +
                lbl(eta, hms(eta), false) +
                lbl(r95[1], hm(r95[1]), true)
        }

        // renderScenarios lists the ETAs of report r at its pace and
        // at those of its scenarios.
        /**
         * @param {HeightReport} r
         * @param {EtaPace} pace
         */
        function renderScenarios(r, pace) {
            const eta = ms(r.eta)
            const chips = [
                {
                    k:
                        'current pace · ' +
                        pace.seconds_per_block.toFixed(3) +
                        ' s',
                    v: onDay(eta, hms(eta)),
                    d: '',
                },
            ]
            ;(r.scenarios || []).forEach(function (s) {
                const dd = (ms(s.eta) - eta) / 1000
                const col =
                    Math.abs(dd) < 60 ? 'fg2' : dd > 0 ? 'yellow' : 'blue'
                chips.push({
                    k:
                        esc(PACE_OF[s.pace_of] || s.pace_of) +
                        ' · ' +
                        s.seconds_per_block.toFixed(3) +
                        ' s',
                    v: onDay(ms(s.eta), hms(ms(s.eta))),
                    d:
                        '<span class="d" style="color: var(--' +
                        col +
                        ')">' +
                        drift(dd) +
                        '</span>',
                })
            })
            q('.eta-scen').innerHTML = chips
                .map(function (c) {
                    return (
                        '<span class="s"><span class="k">' +
                        c.k +
                        '</span><span class="v">' +
                        c.v +
                        '</span>' +
                        c.d +
                        '</span>'
                    )
                })
                .join('')
        }

        // drawChart plots the ETA over time against the original
        // (dashed line), with its 80% range, and a cone from now
        // to the ETA where the range closes.
        function drawChart() {
            // CARD_BODY holds it.
            const svg = /** @type {SVGSVGElement} */ (
                el.querySelector('.eta-chart svg')
            )
            const box = svg.getBoundingClientRect()
            if (!rep || failed(rep) || !box.width) return
            const W = box.width
            const H = box.height || 170
            const L = 40
            const R = 8
            const T = 8
            const B = 20
            svg.setAttribute('viewBox', '0 0 ' + W + ' ' + H)
            const ref = ms(rep.original ? rep.original.eta : rep.eta)
            const pts = (rep.trail || []).map(function (p) {
                const e = ms(p.eta)
                return {
                    t: ms(p.at),
                    eta: e,
                    lo: p.range_80 ? ms(p.range_80.from) : e,
                    hi: p.range_80 ? ms(p.range_80.to) : e,
                }
            })
            const end = ms(rep.eta)
            const now = rep.reached_at ? end : ms(rep.now.block_time)
            const cur = {
                t: now,
                eta: end,
                lo: rep.range_80 ? ms(rep.range_80.from) : end,
                hi: rep.range_80 ? ms(rep.range_80.to) : end,
            }
            pts.push(cur)
            const x0 = Math.min(present(pts[0]).t, now - 3600000)
            const x1 = Math.max(end, now + 60000)
            const ys = [0]
            pts.forEach(function (p) {
                ys.push(p.lo - ref, p.hi - ref)
            })
            let y0 = Math.min.apply(null, ys)
            let y1 = Math.max.apply(null, ys)
            const yp = (y1 - y0) * 0.1 || 60000
            y0 -= yp
            y1 += yp
            // X and Y place a time and an offset from the reference
            // on the chart; mins writes an offset in minutes.
            /**
             * @param {number} v
             * @returns {number}
             */
            function X(v) {
                return L + ((v - x0) / (x1 - x0)) * (W - L - R)
            }
            /**
             * @param {number} v
             * @returns {number}
             */
            function Y(v) {
                return T + (1 - (v - y0) / (y1 - y0)) * (H - T - B)
            }
            /**
             * @param {number} v
             * @returns {string}
             */
            function mins(v) {
                return (
                    (v >= 0 ? '+' : '−') + Math.abs(Math.round(v / 60000)) + 'm'
                )
            }
            // yLabel marks an offset on the left axis, unless it
            // would overlap the original's label.
            /**
             * @param {number} v
             * @returns {string}
             */
            function yLabel(v) {
                if (Math.abs(Y(v) - Y(0)) < 12) return ''
                return (
                    '<text x="' +
                    (L - 4) +
                    '" y="' +
                    (Y(v) + 3) +
                    '" text-anchor="end">' +
                    mins(v) +
                    '</text>'
                )
            }
            // The "now" label goes left of its line near the edge.
            const nearEdge = X(now) > W - R - 50
            const nowLabel =
                '<text x="' +
                (X(now) + (nearEdge ? -4 : 4)) +
                '" y="' +
                (T + 9) +
                '"' +
                (nearEdge ? ' text-anchor="end"' : '') +
                '>' +
                (rep.reached_at ? 'reached' : 'now') +
                '</text>'
            const band = pts
                .map(function (p) {
                    return X(p.t) + ',' + Y(p.hi - ref)
                })
                .concat(
                    pts
                        .slice()
                        .reverse()
                        .map(function (p) {
                            return X(p.t) + ',' + Y(p.lo - ref)
                        })
                )
                .join(' ')
            const line = pts
                .map(function (p) {
                    return X(p.t) + ',' + Y(p.eta - ref)
                })
                .join(' ')
            const cone =
                rep.reached_at || !rep.range_80
                    ? ''
                    : '<polygon class="cone" points="' +
                      X(now) +
                      ',' +
                      Y(cur.hi - ref) +
                      ' ' +
                      X(end) +
                      ',' +
                      Y(end - ref) +
                      ' ' +
                      X(now) +
                      ',' +
                      Y(cur.lo - ref) +
                      '"/>'
            svg.innerHTML =
                '<line class="axis" x1="' +
                L +
                '" y1="' +
                (H - B) +
                '" x2="' +
                (W - R) +
                '" y2="' +
                (H - B) +
                '"/>' +
                '<polygon class="band" points="' +
                band +
                '"/>' +
                cone +
                '<line class="zero" x1="' +
                L +
                '" y1="' +
                Y(0) +
                '" x2="' +
                (W - R) +
                '" y2="' +
                Y(0) +
                '"/>' +
                '<polyline class="line" points="' +
                line +
                '"/>' +
                '<circle cx="' +
                X(now) +
                '" cy="' +
                Y(cur.eta - ref) +
                '" r="4" fill="var(--blue)"/>' +
                '<line class="nowline" x1="' +
                X(now) +
                '" y1="' +
                T +
                '" x2="' +
                X(now) +
                '" y2="' +
                (H - B) +
                '"/>' +
                nowLabel +
                yLabel(y1 - yp) +
                '<text class="orig" x="' +
                (L - 4) +
                '" y="' +
                (Y(0) + 3) +
                '" text-anchor="end">' +
                (rep.original ? 'orig' : 'ETA') +
                '</text>' +
                yLabel(y0 + yp) +
                '<text x="' +
                L +
                '" y="' +
                (H - 6) +
                '">' +
                day(x0) +
                ' ' +
                hm(x0) +
                '</text>' +
                '<text x="' +
                (W - R) +
                '" y="' +
                (H - 6) +
                '" text-anchor="end">' +
                // The right end is the ETA, or the block once
                // reached.
                (rep.reached_at ? 'reached ' : 'ETA ') +
                onDay(end, hm(end)) +
                '</text>'
        }

        // tick refreshes the countdown, the target line and the
        // compact line.
        function tick() {
            if (!rep || failed(rep)) return
            if (rep.reached_at) {
                const at = ms(rep.reached_at)
                q('.eta-count').textContent = 'reached'
                q('.eta-target').textContent = head(rep)
                setInline(
                    'reached',
                    ' · ' +
                        head(rep) +
                        ' · ' +
                        day(at) +
                        ' ' +
                        hms(at) +
                        ' UTC',
                    warning(rep)
                )
                return
            }
            const left = rep.seconds_left - (Date.now() - fetchedAt) / 1000
            // Past the ETA, the block is late: say so rather than
            // freeze at zero until the next poll moves the ETA.
            // The count shows whole seconds: due once they read 0.
            const due = Math.round(left) <= 0
            const count = due ? 'due' : countdown(left)
            const eta = ms(rep.eta)
            const n = rep.target.blocks_left
            q('.eta-count').textContent = count
            q('.eta-target').textContent = !due
                ? 'until ' +
                  head(rep) +
                  ' (' +
                  thousands(n) +
                  (n === 1 ? ' block' : ' blocks') +
                  ' to go)'
                : head(rep) + ' · ETA passed, waiting for the block'
            setInline(
                count,
                ' · ' + head(rep) + ' · ' + day(eta) + ' ' + hm(eta) + ' UTC',
                warning(rep)
            )
        }

        // The compact line's nodes, kept across ticks: a popover
        // open on the mark stays with it.
        const inlineCount = document.createElement('b')
        const inlineText = document.createTextNode('')
        const inlineMark = document.createElement('span')
        inlineMark.className = 'warn tap-popover'
        inlineMark.textContent = '⚠'

        // setInline writes the compact line: the count in bold,
        // then text, then warn, the card's warnings, as a ⚠ mark. It
        // updates the nodes in place.
        /**
         * @param {string} count
         * @param {string} text
         * @param {string} warn
         */
        function setInline(count, text, warn) {
            inlineCount.textContent = count
            inlineText.data = text + (warn ? ' ' : '')
            if (warn && inlineMark.getAttribute('data-popover') !== warn)
                inlineMark.setAttribute('data-popover', warn)
            const parts = warn
                ? [inlineCount, inlineText, inlineMark]
                : [inlineCount, inlineText]
            const line = q('.eta-inline')
            const kept =
                line.childNodes.length === parts.length &&
                parts.every(function (node, i) {
                    return line.childNodes[i] === node
                })
            if (!kept) line.replaceChildren(...parts)
        }

        return {
            show: show,
            hide: hide,
            tick: tick,
            // counting tells whether the card counts down to its
            // target: it has an answer, neither failed nor reached.
            counting: function () {
                return !!rep && !failed(rep) && !rep.reached_at
            },
            redraw: function () {
                drawChart()
                renderRange()
            },
        }
    }

    const haltCard = etaCard('eta-halt-card', 'halt')
    const linkCard = etaCard('eta-link-card', 'link')
    // lastHalt is the scheduled halt the polls last read, as
    // /api/eta sent it, unchecked; null without one.
    /** @type {EtaHalt | null} */
    let lastHalt = null

    // The link target lives in the page URL: ?eta=<height>
    // &since=<when the countdown was set>&label=<text>.
    /** @typedef {{ height: string, since: string, label: string }} LinkTarget */
    /** @returns {LinkTarget | null} */
    function linkTarget() {
        const p = new URLSearchParams(location.search)
        const height = p.get('eta')
        if (!height) return null
        return {
            height: height,
            since: p.get('since') || '',
            // Capped like the planner's input: a link can carry more.
            label: Array.from(p.get('label') || '')
                .slice(0, 40)
                .join(''),
        }
    }
    // setLinkTarget makes t the page link's target, none for null,
    // and shows its card afresh.
    /** @param {LinkTarget | null} t */
    function setLinkTarget(t) {
        const p = new URLSearchParams(location.search)
        ;['eta', 'since', 'label'].forEach(function (k) {
            p.delete(k)
        })
        if (t) {
            p.set('eta', t.height)
            p.set('since', t.since)
            if (t.label) p.set('label', t.label)
        }
        const s = p.toString()
        history.replaceState(
            null,
            '',
            location.pathname + (s ? '?' + s : '') + location.hash
        )
        // A poll in flight answers for the old target: drop it,
        // and start the card afresh.
        pollSeq++
        linkCard.hide()
        closeRemind(false)
        renderRemind()
        pollETA()
    }

    // haltError is why the halt status is unknown, when the last
    // poll failed; pollSeq changes with the link target, so a
    // poll in flight for the old one drops its answers.
    let haltError = ''
    let pollSeq = 0

    // pollETA refreshes both cards. A failed answer leaves the
    // last good one on screen, marked stale.
    /** @returns {Promise<void>} */
    async function pollETA() {
        const seq = pollSeq
        const h = await getETA('')
        if (seq !== pollSeq) return
        if (failed(h)) {
            haltError = h.error
            // The server names a scheduled halt whose estimate
            // failed; a lost answer says nothing of it.
            if (h.halt) {
                // The card's answer for a halt that moved is
                // not one to keep on screen.
                if (lastHalt && lastHalt.height !== h.halt.height)
                    haltCard.hide()
                lastHalt = h.halt
            }
            if (lastHalt) haltCard.show({ error: h.error, halt: lastHalt })
        } else {
            haltError = ''
            lastHalt = h.halt || null
            // With a halt, the question is its height's.
            if (h.halt) haltCard.show(/** @type {HeightReport} */ (h))
            else haltCard.hide()
        }
        // The planner's halt row follows each poll.
        renderHaltRow()
        const t = linkTarget()
        if (
            !t ||
            (lastHalt && lastHalt.height === Number(t.height) && !mine())
        ) {
            linkCard.hide()
            renderRemind()
            return
        }
        const p = new URLSearchParams({ height: t.height })
        if (t.since) p.set('since', t.since)
        // The link's question is a height's.
        const rep = /** @type {HeightAnswer} */ (await getETA(p.toString()))
        if (seq !== pollSeq) return
        linkCard.show(rep, t.label)
        renderRemind()
        if (live.vapidKey) loadReminder()
    }

    const linkEl = $('eta-link-card')
    $in(linkEl, '.eta-copy').addEventListener('click', function (e) {
        copyNotify(location.href, e)
    })
    $in(linkEl, '.eta-close').addEventListener('click', async function () {
        // A reminder that cannot be removed keeps its card:
        // closing it would hide a reminder still due to fire.
        if (mine() && !(await removeReminder())) {
            openRemind()
            remindError(
                'Could not remove the reminder, so the card stays. See console for details.'
            )
            return
        }
        setLinkTarget(null)
    })

    // ---- Reminder
    // The link card's notify button reminds this device before its
    // height (/api/push/reminder): one reminder per device. Shown
    // only where push works (initPush got the server's key). A
    // page opened without a target shows the reminder's card: the
    // Home Screen app always opens at /. The reminder is read again
    // at each ETA refresh: the server drops it once it fired.
    const remindWrap = $in(linkEl, '.eta-remind-wrap')
    const remindBtn = /** @type {HTMLButtonElement} */ (
        $in(remindWrap, '.eta-remind')
    )
    const remindPop = $in(remindWrap, '.eta-remind-pop')
    // reminder is this device's, as /api/push/reminder sent it,
    // unchecked: { height, since, label, lead }; null without one.
    /** @type {ReminderView | null} */
    let reminder = null

    // rq is the popover's element sel matches (index.html).
    /**
     * @param {string} sel
     * @returns {HTMLElement}
     */
    function rq(sel) {
        return $in(remindPop, sel)
    }
    // leadText words a lead as the server writes it: "30m" →
    // "30 min", "1h" → "1 h", "100 blocks" and "1 block" stay.
    /**
     * @param {string} lead
     * @returns {string}
     */
    function leadText(lead) {
        const m = /^(\d+)([mh])$/.exec(lead)
        if (!m) return lead
        return m[1] + (m[2] === 'm' ? ' min' : ' h')
    }
    // leadInputs reads a lead as the server writes it ("30m",
    // "1h", "1 block", "100 blocks") into the popover's fields.
    /**
     * @param {string} lead
     * @returns {{ n: number, unit: string }}
     */
    function leadInputs(lead) {
        const m = /^(\d+)( blocks?|m|h)$/.exec(lead || '')
        if (!m) return { n: 30, unit: 'm' }
        const unit = present(m[2])
        if (unit.startsWith(' block'))
            return { n: Number(m[1]), unit: 'blocks' }
        return {
            n: Number(m[1]) * (unit === 'h' ? 60 : 1),
            unit: 'm',
        }
    }
    // mine tells whether the device's reminder is the link card's.
    /** @returns {boolean} */
    function mine() {
        const t = linkTarget()
        return !!(t && reminder && reminder.height === Number(t.height))
    }
    // since is the card's start as RFC 3339 in UTC, to the second
    // (the server takes no other form): the link's, or the
    // server's time when the link carries none it can read.
    /**
     * @param {LinkTarget} t
     * @returns {string}
     */
    function since(t) {
        const linked = Date.parse(t.since)
        const at = Number.isNaN(linked)
            ? Date.now() + live.serverTimeOffset
            : linked
        return new Date(at).toISOString().replace(/\.\d+Z$/, 'Z')
    }
    // renderRemind offers the button where push works, on a card
    // counting down: a reached target or a failed answer has
    // nothing to notify before, unless it is this device's
    // reminder, which stays to be seen and removed.
    function renderRemind() {
        remindWrap.hidden = !live.vapidKey || !(linkCard.counting() || mine())
        if (remindWrap.hidden) closeRemind(false)
        const set = mine()
        remindBtn.classList.toggle('set', set)
        $in(remindBtn, '.eta-remind-text').textContent =
            set && reminder ? 'notify · ' + leadText(reminder.lead) : 'notify'
        // An open popover follows the reminder read; the lead
        // typed in it stays.
        if (!remindPop.hidden) renderRemindPop()
    }
    // renderRemindPop words the popover after the reminder:
    // Update and Remove for the card's own, a warning before
    // replacing one for another height.
    function renderRemindPop() {
        // The popover opens only on a card with a target
        // (openRemind), and setLinkTarget closes it as the target
        // goes.
        const t = /** @type {LinkTarget} */ (linkTarget())
        rq('.eta-remind-before').textContent =
            'before #' + thousands(Number(t.height))
        const warn = rq('.warn')
        warn.hidden = !reminder || mine()
        if (reminder && !warn.hidden)
            warn.textContent =
                'Replaces your reminder for #' +
                thousands(reminder.height) +
                '.'
        if (!saving) rq('.eta-remind-save').textContent = saveLabel()
        const remove = rq('.eta-remind-remove')
        // Hidden, Remove would lose the keyboard: Save takes it,
        // or the lead field while a save keeps Save off.
        if (!mine() && document.activeElement === remove) {
            const save = /** @type {HTMLButtonElement} */ (
                rq('.eta-remind-save')
            )
            if (save.disabled) rq('.eta-remind-n').focus()
            else save.focus()
        }
        remove.hidden = !mine()
    }
    // saving is set while a save runs: Save reads Saving… then.
    let saving = false
    // saveLabel is Save's label: Update for the card's own
    // reminder.
    /** @returns {string} */
    function saveLabel() {
        return mine() ? 'Update' : 'Save'
    }
    function openRemind() {
        const t = linkTarget()
        if (!t) return
        const v = leadInputs(mine() && reminder ? reminder.lead : '')
        const n = /** @type {HTMLInputElement} */ (rq('.eta-remind-n'))
        n.value = String(v.n)
        const unit = /** @type {HTMLSelectElement} */ (rq('.eta-remind-unit'))
        unit.value = v.unit
        renderRemindPop()
        rq('.err').hidden = true
        remindPop.hidden = false
        remindBtn.setAttribute('aria-expanded', 'true')
        rq('.eta-remind-n').focus()
    }
    // closeRemind hides the popover; refocus returns the focus
    // to the button, for keyboard users.
    /** @param {boolean} refocus */
    function closeRemind(refocus) {
        if (remindPop.hidden) return
        remindPop.hidden = true
        remindBtn.setAttribute('aria-expanded', 'false')
        if (refocus) remindBtn.focus()
    }
    // remindError says msg in the popover.
    /** @param {string} msg */
    function remindError(msg) {
        const e = rq('.err')
        e.textContent = msg
        e.hidden = false
    }
    // reminderReads numbers the reads of the reminder, and
    // readApplied is the newest one applied: polls do not wait for
    // theirs, so an answer older than the one applied is dropped.
    let reminderReads = 0
    let readApplied = 0
    // loadReminder reads this device's reminder. A failed read
    // changes nothing: it says nothing of the server's.
    /** @returns {Promise<void>} */
    async function loadReminder() {
        const read = ++reminderReads
        /** @type {ReminderView | null | undefined} */
        let next = null
        const id = live.pushSubID
        if (id) {
            try {
                // A refused read reads as undefined.
                next = await boundedRead(
                    answerTimeoutMs,
                    async function (signal) {
                        // Past the browser's cache: while a
                        // cached read is out, Chromium holds the
                        // next read of the address and fails its
                        // removal.
                        const r = await fetch(
                            '/api/push/reminder?id=' + encodeURIComponent(id),
                            { signal, cache: 'no-store' }
                        )
                        if (!r.ok) {
                            console.warn('read reminder:', r.status)
                            return undefined
                        }
                        // 204: the device has no reminder.
                        if (r.status === 204) return null
                        /** @type {ReminderView} */
                        const d = await r.json()
                        return d
                    }
                )
            } catch (e) {
                console.warn('read reminder:', e)
                return
            }
            if (next === undefined) return
        }
        applyReminder(read, next)
    }
    // applyReminder makes r the reminder, as the read numbered
    // read gave it, unless a newer read was applied.
    /**
     * @param {number} read
     * @param {ReminderView | null} r
     */
    function applyReminder(read, r) {
        if (read < readApplied) return
        readApplied = read
        reminder = r
        renderRemind()
    }
    /** @returns {Promise<void>} */
    async function saveReminder() {
        const t = linkTarget()
        // A whole number from 1: the field also takes decimals
        // and exponents, which a lead does not.
        const typed = /** @type {HTMLInputElement} */ (rq('.eta-remind-n'))
            .value
        const n = Number(typed)
        if (!t || !/^\d+$/.test(typed) || n < 1) {
            remindError('Enter how far ahead, 1 or more.')
            return
        }
        // Past 2^53 a number is not exact: the lead sent would
        // not be the one typed.
        if (!Number.isSafeInteger(n)) {
            remindError('That is too far ahead.')
            return
        }
        const unit = /** @type {HTMLSelectElement} */ (rq('.eta-remind-unit'))
            .value
        const lead = unit === 'blocks' ? n + ' blocks' : n + 'm'
        // Save is off while the save runs: one click, one save.
        const save = /** @type {HTMLButtonElement} */ (rq('.eta-remind-save'))
        saving = true
        save.disabled = true
        save.textContent = 'Saving\u2026'
        let saved = false
        try {
            saved = await putReminder(t, lead)
        } finally {
            saving = false
            save.disabled = false
            save.textContent = saveLabel()
        }
        if (!saved) {
            // Disabled, Save lost the keyboard: it gets it back
            // while the popover stays open.
            if (!remindPop.hidden) save.focus()
            return
        }
        closeRemind(true)
        await loadReminder()
    }
    // putReminder sets this device's reminder for the card t,
    // lead ahead, and reports whether it did; the popover says
    // why it did not.
    /**
     * @param {LinkTarget} t
     * @param {string} lead
     * @returns {Promise<boolean>}
     */
    async function putReminder(t, lead) {
        try {
            if (Notification.permission !== 'granted') {
                if ((await Notification.requestPermission()) !== 'granted') {
                    remindError('Notification permission denied.')
                    return false
                }
            }
            const sub = await browserPushSub(true)
            const body = Object.assign(deviceKeys(sub), {
                height: Number(t.height),
                since: since(t),
                label: t.label,
                lead: lead,
            })
            // The server's answer is waited for answerTimeoutMs
            // at most; a refused save reads as no id.
            const id = await boundedRead(
                answerTimeoutMs,
                async function (signal) {
                    const r = await fetch('/api/push/reminder', {
                        method: 'PUT',
                        headers: {
                            'Content-Type': 'application/json',
                        },
                        body: JSON.stringify(body),
                        signal,
                    })
                    if (!r.ok) {
                        remindError((await r.text()).trim())
                        return null
                    }
                    /** @type {DeviceIDAnswer} */
                    const d = await r.json()
                    return d.id
                }
            )
            if (id === null) return false
            await keepDevice(id)
            return true
        } catch (e) {
            console.warn('save reminder:', e)
            remindError(saveFailure(e))
            return false
        }
    }
    // removeReminder removes this device's reminder, and reports
    // whether it is gone. A device the page forgot has none left
    // on the server. Once gone, the reminder is known to be none:
    // no read is waited for to say so.
    /** @returns {Promise<boolean>} */
    async function removeReminder() {
        if (live.pushSubID && !(await deleteReminder(live.pushSubID)))
            return false
        applyReminder(++reminderReads, null)
        return true
    }
    // deleteReminder deletes the reminder of this device, whose id
    // is id, on the server, and reports whether it is gone: once
    // the server took the removal, even if the browser fails to end
    // its push subscription.
    /**
     * @param {string} id
     * @returns {Promise<boolean>}
     */
    async function deleteReminder(id) {
        let kept
        try {
            kept = await boundedRead(answerTimeoutMs, async function (signal) {
                const r = await fetch(
                    '/api/push/reminder?id=' + encodeURIComponent(id),
                    { method: 'DELETE', signal }
                )
                if (!r.ok) throw new Error(r.status + ' ' + (await r.text()))
                /** @type {ReminderRemovalAnswer} */
                const d = await r.json()
                return d.device_kept
            })
        } catch (e) {
            console.warn('remove reminder:', e)
            return false
        }
        if (!kept) {
            try {
                await keepDevice('')
            } catch (e) {
                console.warn('end push subscription:', e)
            }
        }
        return true
    }
    remindBtn.addEventListener('click', function (e) {
        e.stopPropagation()
        if (remindPop.hidden) openRemind()
        else closeRemind(true)
    })
    remindPop.addEventListener('click', function (e) {
        e.stopPropagation()
    })
    rq('.eta-remind-save').addEventListener('click', saveReminder)
    rq('.eta-remind-remove').addEventListener('click', async function () {
        if (await removeReminder()) closeRemind(true)
        else remindError('Failed to remove. See console for details.')
    })
    rq('.eta-remind-cancel').addEventListener('click', function () {
        closeRemind(true)
    })
    document.addEventListener('click', function () {
        closeRemind(false)
    })
    document.addEventListener('keydown', function (e) {
        if (e.key === 'Escape') closeRemind(true)
    })
    // Once initPush has settled: show the button where push
    // works, and bring back the reminder's card on a page opened
    // without a target. A first poll that read the halt before
    // the reminder hid the card of the halt's height, which the
    // reminder keeps: it shows at once, not at the next poll.
    /** @returns {Promise<void>} */
    async function startReminder() {
        await whenPushReady()
        if (!live.vapidKey) return
        await loadReminder()
        if (reminder && !linkTarget())
            setLinkTarget({
                height: String(reminder.height),
                since: reminder.since,
                label: reminder.label,
            })
        else if (
            mine() &&
            reminder &&
            lastHalt &&
            lastHalt.height === reminder.height
        )
            pollETA()
    }
    startReminder()

    // ---- Planner modal
    let mode = 'height'
    // planned is the height the planner's answer offers a card for;
    // null without one.
    /** @type {number | null} */
    let planned = null
    let planTimer = 0
    let planSeq = 0
    const modal = $('eta-modal')
    const planInput = /** @type {HTMLInputElement} */ ($('eta-in'))
    const planGo = /** @type {HTMLButtonElement} */ ($('eta-go'))
    function openEtaModal() {
        modal.classList.add('open')
        planInput.focus()
        plan()
    }
    function closeEtaModal() {
        modal.classList.remove('open')
        $('eta-btn').focus()
    }
    $('eta-btn').addEventListener('click', openEtaModal)
    $('eta-close').addEventListener('click', closeEtaModal)
    modal.addEventListener('click', function (e) {
        if (e.target === modal) closeEtaModal()
    })
    document.addEventListener('keydown', function (e) {
        if (!modal.classList.contains('open')) return
        if (e.key === 'Escape') closeEtaModal()
        if (e.key === 'Tab') keepFocusIn(modal, e)
    })
    modal.querySelectorAll('.eta-seg button').forEach(function (b) {
        b.addEventListener('click', function () {
            // Each segment button carries data-mode (index.html).
            mode = /** @type {string} */ (b.getAttribute('data-mode'))
            modal.querySelectorAll('.eta-seg button').forEach(function (x) {
                x.classList.toggle('on', x === b)
                x.setAttribute('aria-pressed', String(x === b))
            })
            $('eta-in-label').textContent =
                mode === 'height' ? 'block height' : 'date and time (UTC)'
            planInput.placeholder =
                mode === 'height' ? 'e.g. 1651200' : 'e.g. 2026-11-18 13:00'
            planInput.value = ''
            plan()
        })
    })
    planInput.addEventListener('input', function () {
        // The shown answer no longer matches the input.
        planned = null
        planGo.disabled = true
        clearTimeout(planTimer)
        planTimer = setTimeout(plan, 300)
    })

    // renderHaltRow says in the planner whether a halt is scheduled.
    function renderHaltRow() {
        const line = $('eta-halt-row')
        if (lastHalt) {
            const height = document.createElement('b')
            height.textContent = '#' + thousands(lastHalt.height)
            // A halt may set no minimum version.
            const min = lastHalt.min_version
            line.replaceChildren(
                'On chain: halt scheduled at ',
                height,
                (min ? ' (min ' + min + ')' : '') +
                    '. Its countdown shows for everyone, no link needed.'
            )
        } else if (haltError) {
            line.textContent =
                'On chain: halt status unavailable (' + haltError + ').'
        } else {
            line.textContent = 'On chain: no halt scheduled.'
        }
    }

    // plan answers the planner's input through /api/eta.
    /** @returns {Promise<void>} */
    async function plan() {
        const v = planInput.value.trim()
        const out = $('eta-result')
        const seq = ++planSeq
        planned = null
        planGo.disabled = true
        renderHaltRow()
        if (!v) {
            out.textContent =
                mode === 'height'
                    ? 'Enter a block height to see when it is expected.'
                    : 'Enter a UTC date and time to see which block it reaches.'
            return
        }
        let query
        if (mode === 'height') {
            const h = v.replace(/[#,_\s]/g, '')
            if (!/^\d+$/.test(h)) {
                out.textContent = 'A block height is a whole number.'
                return
            }
            query = 'height=' + h
        } else {
            // A pasted RFC 3339 time keeps its fraction of a
            // second and its Z or offset; " UTC" names the zone
            // of a time written without one.
            const bare = v.replace(/\s*UTC$/i, '')
            const m = bare.match(
                /^(\d{4}-\d{2}-\d{2})[ T](\d{2}:\d{2})(:\d{2}(?:\.\d+)?)?(Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)?$/
            )
            if (!m || (bare !== v && m[4])) {
                out.textContent = 'Use YYYY-MM-DD HH:MM, in UTC.'
                return
            }
            query =
                'time=' +
                encodeURIComponent(
                    m[1] + 'T' + m[2] + (m[3] || ':00') + (m[4] || 'Z')
                )
        }
        const rep = await getETA(query)
        if (seq !== planSeq) return
        if (failed(rep)) {
            out.textContent = rep.error
            return
        }
        const warn = rep.warning
            ? '<div class="eta-warn">' + esc(rep.warning) + '</div>'
            : ''
        if (mode === 'time') {
            // A time's report has its estimate (fillETATime).
            const e = /** @type {EtaHeightEstimate} */ (rep.estimate)
            out.innerHTML =
                '<div class="big">≈ #' +
                thousands(e.height) +
                '</div>' +
                (e.range_80 && e.range_95
                    ? '80% #' +
                      thousands(e.range_80.from) +
                      '–#' +
                      thousands(e.range_80.to) +
                      ' · 95% #' +
                      thousands(e.range_95.from) +
                      '–#' +
                      thousands(e.range_95.to)
                    : 'margin pending: ' + HISTORY_SHORT) +
                warn
            planned = e.height
        } else if (rep.reached_at) {
            out.innerHTML =
                '<div class="big">Reached ' +
                day(ms(rep.reached_at)) +
                ' ' +
                hms(ms(rep.reached_at)) +
                ' UTC</div>'
            return
        } else {
            // A height's report (fillETAHeight).
            const h = /** @type {HeightReport} */ (rep)
            out.innerHTML =
                '<div class="big">' +
                day(ms(h.eta)) +
                ' ' +
                hms(ms(h.eta)) +
                ' UTC</div>' +
                // Under a minute, minutes would read 0; a
                // stalled chain leaves its ETA behind.
                (h.seconds_left >= 60
                    ? 'in ' + inShort(h.seconds_left)
                    : h.seconds_left > 0
                      ? 'in &lt;1m'
                      : 'ETA passed, waiting for the block') +
                (h.range_80 && h.range_95
                    ? ' · 80% ' +
                      rangeDays(h.range_80) +
                      ' · 95% ' +
                      rangeDays(h.range_95)
                    : ' · margin pending: ' + HISTORY_SHORT) +
                warn
            planned = h.target.height
        }
        planGo.disabled = false
    }

    planGo.addEventListener('click', function () {
        if (planned === null) return
        // since is in server time, so the original estimate is
        // the one shown now even when this clock is off.
        const since = new Date(Date.now() + live.serverTimeOffset)
            .toISOString()
            .replace(/\.\d+Z$/, 'Z')
        setLinkTarget({
            height: String(planned),
            since: since,
            label: /** @type {HTMLInputElement} */ (
                $('eta-label-in')
            ).value.trim(),
        })
        closeEtaModal()
    })

    // Each poll is scheduled once the previous one is done, so a
    // slow answer is shown rather than overtaken.
    ;(async function pollLoop() {
        await pollETA()
        setTimeout(pollLoop, ETA_POLL_MS)
    })()
    setInterval(function () {
        haltCard.tick()
        linkCard.tick()
    }, 1000)
    window.addEventListener('resize', function () {
        haltCard.redraw()
        linkCard.redraw()
    })
}
