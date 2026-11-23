// The map card: the peers placed on a world map, and beside it their
// count by country or by provider.

/** @import { Peer } from '../types/node.js' */
/** @import { PopRow } from './popover.js' */

import { compareNames } from './collate.js'
import { countryFlag, countryName } from './countries.js'
import { refreshCountryHighlight } from './country-highlight.js'
import { $, $svg, clearChildren, SVG_NS } from './dom.js'
import { pointedElement } from './pointer.js'
import { popRows } from './popover.js'
import { present } from './present.js'
import { live } from './state.js'

/**
 * The peers counted by country or by provider.
 * @typedef {Record<string, number>} Counts
 */
/**
 * The validators (v) and the other peers (p) of a branch of a cluster's
 * popover.
 * @typedef {{ v: number, p: number }} BranchCount
 */
/**
 * A cluster of the map: its group and its kind, how many peers it
 * holds, the sums of their coordinates, its place (x, y) and radius,
 * and its anchor, the place its peers are at (ax, ay).
 * @typedef {object} MapPoint
 * @property {string} grp
 * @property {'val' | 'peer'} type
 * @property {number} count
 * @property {number} sx
 * @property {number} sy
 * @property {number} x
 * @property {number} y
 * @property {number} r
 * @property {number} ax
 * @property {number} ay
 */

// mapFingerprint sums up what the map and its side list draw of
// peers: each peer's place, kind, country and provider, and the
// list's mode.
/**
 * @param {Peer[] | null} peers
 * @returns {string}
 */
function mapFingerprint(peers) {
    return JSON.stringify([
        mapListMode,
        (peers || []).map(function (p) {
            return [p.lat, p.lon, p.country, p.provider, p.val_address, p.role]
        }),
    ])
}
/** @type {string | null} */
let lastMapFingerprint = null

// renderMap plots a dot for each peer that has a geolocation, on an
// equirectangular world map (viewBox 0 0 360 180): x = lon + 180,
// y = 90 - lat. Dots and the side list are tagged with the peer's
// country so hovering either highlights the other. The card stays
// hidden until at least one peer resolves (the geo database
// downloads in the background at startup). It draws again only
// when what it draws changed: an open popover, a highlight and the
// list's scroll stay.
/** @param {Peer[] | null} peers */
export function renderMap(peers) {
    const card = $('map-card')
    const g = $svg('map-points')
    const list = $('map-country-list')
    const fp = mapFingerprint(peers)
    if (fp === lastMapFingerprint) return
    lastMapFingerprint = fp
    clearChildren(g)
    clearChildren(list)

    const allPeers = peers || []
    // The server sends a peer's coordinates only once it is
    // located, and 0 is a coordinate like any other.
    const located = allPeers.filter(
        /**
         * @param {Peer} p
         * @returns {p is Peer & { lat: number, lon: number }}
         */
        function (p) {
            return typeof p.lat === 'number' && typeof p.lon === 'number'
        }
    )
    if (located.length === 0) {
        card.style.display = 'none'
        return
    }
    card.style.display = ''

    // Aggregate ALL peers (located + unlocated) for the side lists so
    // peers with no public IP (hence no country/provider) are counted
    // under "Unknown". Map dots are plotted only for located peers.
    /** @type {Counts} */
    const byCountry = {}
    /** @type {Counts} */
    const byProvider = {}
    allPeers.forEach(function (p) {
        const code = p.country || 'Unknown'
        byCountry[code] = (byCountry[code] || 0) + 1
        const prov = p.provider || 'Unknown'
        byProvider[prov] = (byProvider[prov] || 0) + 1
    })
    // Per-country validator / peer counts among located peers, for the
    // cluster tooltips.
    /**
     * @param {Peer} p
     * @returns {boolean}
     */
    function isVal(p) {
        return !!p.val_address || (p.role || '').indexOf('val') === 0
    }
    // Totals + cross breakdowns among located peers, keyed both ways
    // for the two cluster tooltips: country root -> providers, and
    // provider root -> countries.
    /** @type {Counts} */
    const ccVal = {}
    /** @type {Counts} */
    const ccPeer = {}
    /** @type {Record<string, Record<string, BranchCount>>} */
    const ccProv = {} // cc -> { provider -> {v, p} }
    /** @type {Counts} */
    const prVal = {}
    /** @type {Counts} */
    const prPeer = {}
    /** @type {Record<string, Record<string, BranchCount>>} */
    const prCC = {} // provider -> { cc -> {v, p} }
    located.forEach(function (p) {
        const cc = p.country || 'Unknown'
        const prov = p.provider || 'Unknown'
        const v = isVal(p)
        if (v) {
            ccVal[cc] = (ccVal[cc] || 0) + 1
            prVal[prov] = (prVal[prov] || 0) + 1
        } else {
            ccPeer[cc] = (ccPeer[cc] || 0) + 1
            prPeer[prov] = (prPeer[prov] || 0) + 1
        }
        const m1 = (ccProv[cc] = ccProv[cc] || {})
        const e1 = (m1[prov] = m1[prov] || { v: 0, p: 0 })
        const m2 = (prCC[prov] = prCC[prov] || {})
        const e2 = (m2[cc] = m2[cc] || { v: 0, p: 0 })
        if (v) {
            e1.v++
            e2.v++
        } else {
            e1.p++
            e2.p++
        }
    })
    // "N val(s) · M peer(s)", omitting a zero side — shared by the
    // popover's title and its branch rows for consistency.
    /**
     * @param {number} v
     * @param {number} p
     * @returns {string}
     */
    function counts(v, p) {
        const parts = []
        if (v) parts.push(v + ' val' + (v === 1 ? '' : 's'))
        if (p) parts.push(p + ' peer' + (p === 1 ? '' : 's'))
        return parts.join(' · ')
    }
    // Branch rows under a popover's title, most nodes first,
    // ties in the order of the names they show (compareNames),
    // Unknown last as in the side list. name(key) is the name a
    // branch shows, label(key) its whole label.
    /**
     * @param {Record<string, BranchCount> | undefined} m
     * @param {(key: string) => string} name
     * @param {(key: string) => string} label
     * @returns {PopRow[]}
     */
    function branchRows(m, name, label) {
        if (!m) return []
        return Object.entries(m)
            .sort(function ([a, ca], [b, cb]) {
                if (a === 'Unknown') return 1
                if (b === 'Unknown') return -1
                const ta = ca.v + ca.p,
                    tb = cb.v + cb.p
                return tb - ta || compareNames(name(a), name(b))
            })
            .map(function ([k, c]) {
                return [null, label(k), counts(c.v, c.p)]
            })
    }
    // Country popover: flag + name + totals, providers as rows.
    /**
     * @param {Element} el
     * @param {string} cc
     */
    function countryPop(el, cc) {
        popRows(
            el,
            countryFlag(cc) +
                ' ' +
                countryName(cc) +
                ' · ' +
                counts(ccVal[cc] || 0, ccPeer[cc] || 0),
            branchRows(
                ccProv[cc],
                (pr) => pr,
                (pr) => '☁️ ' + pr
            )
        )
    }
    // Provider popover: cloud + name + totals, countries as rows.
    /**
     * @param {Element} el
     * @param {string} prov
     */
    function providerPop(el, prov) {
        popRows(
            el,
            '☁️ ' + prov + ' · ' + counts(prVal[prov] || 0, prPeer[prov] || 0),
            branchRows(prCC[prov], countryName, function (cc) {
                return countryFlag(cc) + ' ' + countryName(cc)
            })
        )
    }

    // The map mirrors the side list's mode: clusters group by country
    // in Country mode and by provider in Provider mode.
    const provMode = mapListMode === 'provider'

    // One point per (city coordinate, type, group): city-level geo puts
    // every node in a city on one coordinate, split into validators vs
    // peers, keyed by the active grouping.
    /** @type {Record<string, MapPoint>} */
    const byKey = {}
    located.forEach(function (p) {
        /** @type {'val' | 'peer'} */
        const type = isVal(p) ? 'val' : 'peer'
        const grp = provMode ? p.provider || 'Unknown' : p.country || 'Unknown'
        const cx = p.lon + 180,
            cy = 90 - p.lat
        const key = cx.toFixed(3) + ',' + cy.toFixed(3) + ',' + type + ',' + grp
        let pt = byKey[key]
        if (!pt)
            pt = byKey[key] = {
                grp: grp,
                type: type,
                count: 0,
                sx: 0,
                sy: 0,
                // fin places it.
                x: 0,
                y: 0,
                r: 0,
                ax: 0,
                ay: 0,
            }
        pt.count++
        pt.sx += cx
        pt.sy += cy
    })
    const pts = Object.values(byKey)
    const GAP = 4
    // fin places cluster p at the mean of its peers' coordinates, its
    // anchor there too, sized by its count.
    /** @param {MapPoint} p */
    function fin(p) {
        p.x = p.sx / p.count
        p.y = p.sy / p.count
        p.r = 1.4 + 0.75 * Math.sqrt(p.count)
        p.ax = p.x
        p.ay = p.y
    }
    pts.forEach(fin)

    // Merge same-group, same-type points that collide — even across
    // cities — into one cluster; repeat until stable.
    let changed = true
    while (changed) {
        changed = false
        for (let i = 0; i < pts.length && !changed; i++) {
            for (let j = i + 1; j < pts.length; j++) {
                const a = present(pts[i]),
                    b = present(pts[j])
                if (a.grp !== b.grp || a.type !== b.type) continue
                const dx = b.x - a.x,
                    dy = b.y - a.y
                if (Math.sqrt(dx * dx + dy * dy) < a.r + b.r + GAP) {
                    a.sx += b.sx
                    a.sy += b.sy
                    a.count += b.count
                    fin(a)
                    pts.splice(j, 1)
                    changed = true
                    break
                }
            }
        }
    }

    // Declutter what still overlaps (different groups, or validators
    // vs peers at one city): push apart, spring back toward the anchor.
    for (let it = 0; it < 80; it++) {
        for (let x = 0; x < pts.length; x++) {
            for (let y = x + 1; y < pts.length; y++) {
                const p1 = present(pts[x]),
                    p2 = present(pts[y])
                const ex = p2.x - p1.x,
                    ey = p2.y - p1.y
                const d = Math.sqrt(ex * ex + ey * ey) || 0.001,
                    mn = p1.r + p2.r + GAP
                if (d < mn) {
                    const push = (mn - d) / 2,
                        ux = ex / d,
                        uy = ey / d
                    p1.x -= ux * push
                    p1.y -= uy * push
                    p2.x += ux * push
                    p2.y += uy * push
                }
            }
        }
        pts.forEach(function (p) {
            p.x += (p.ax - p.x) * 0.08
            p.y += (p.ay - p.y) * 0.08
        })
    }

    // Every element of a cluster (line, anchor, dot, label) carries the
    // group attribute — data-cc in Country mode, data-prov in Provider
    // mode — so hover-highlighting spares whole clusters when the rest
    // of the map dims. The "Unknown" group has no side-list row to link
    // to, so it carries no attribute (it just dims like the rest).
    const grpAttr = provMode ? 'data-prov' : 'data-cc'
    /**
     * @param {Element} el
     * @param {MapPoint} p
     */
    function tagGroup(el, p) {
        if (p.grp !== 'Unknown') el.setAttribute(grpAttr, p.grp)
    }

    // Leader lines + anchor dots first, so they sit under the clusters.
    pts.forEach(function (p) {
        const off = Math.sqrt(
            (p.x - p.ax) * (p.x - p.ax) + (p.y - p.ay) * (p.y - p.ay)
        )
        if (off <= p.r * 0.5 + 0.3) return
        const line = document.createElementNS(SVG_NS, 'line')
        line.setAttribute('x1', p.ax.toFixed(2))
        line.setAttribute('y1', p.ay.toFixed(2))
        line.setAttribute('x2', p.x.toFixed(2))
        line.setAttribute('y2', p.y.toFixed(2))
        line.setAttribute(
            'class',
            p.type === 'val' ? 'map-leader-val' : 'map-leader-peer'
        )
        tagGroup(line, p)
        g.appendChild(line)
        const anch = document.createElementNS(SVG_NS, 'circle')
        anch.setAttribute('cx', p.ax.toFixed(2))
        anch.setAttribute('cy', p.ay.toFixed(2))
        anch.setAttribute('r', '0.5')
        anch.setAttribute('class', 'map-anchor')
        tagGroup(anch, p)
        g.appendChild(anch)
    })

    // Clusters: blue validators / green peers, labelled with the count.
    // The popover is titled with the active grouping and lists its
    // branches: a country's providers in Country mode, a provider's
    // countries in Provider mode.
    pts.forEach(function (p) {
        const c = document.createElementNS(SVG_NS, 'circle')
        c.setAttribute('cx', p.x.toFixed(2))
        c.setAttribute('cy', p.y.toFixed(2))
        c.setAttribute('r', p.r.toFixed(2))
        c.setAttribute('class', p.type === 'val' ? 'map-dot is-val' : 'map-dot')
        tagGroup(c, p)
        if (provMode) providerPop(c, p.grp)
        else countryPop(c, p.grp)
        g.appendChild(c)
        if (p.count > 1) {
            const t = document.createElementNS(SVG_NS, 'text')
            t.setAttribute('x', p.x.toFixed(2))
            t.setAttribute('y', p.y.toFixed(2))
            t.setAttribute('class', 'map-count')
            t.setAttribute('font-size', (p.r * 0.95).toFixed(2))
            t.textContent = String(p.count)
            tagGroup(t, p)
            g.appendChild(t)
        }
    })

    lastByCountry = byCountry
    lastByProvider = byProvider
    renderSideList()
    refreshCountryHighlight(pointedElement())
}

// Side list mode: "country" (default) or "provider", toggled by the
// buttons above the list. Both aggregations are cached from the last
// renderMap so switching re-renders without refetching.
let mapListMode = 'country'
/** @type {Counts} */
let lastByCountry = {}
/** @type {Counts} */
let lastByProvider = {}

// renderSideList lists the last map's peers by the list's mode.
function renderSideList() {
    const list = $('map-country-list')
    clearChildren(list)
    if (mapListMode === 'provider') renderProviderList(list, lastByProvider)
    else renderCountryList(list, lastByCountry)
}

// renderProviderList builds one row per cloud provider, count desc,
// ties in name order (compareNames), Unknown last. Rows carry
// data-prov so hovering them cross-highlights the Provider columns
// in the peer/validator tables.
/**
 * @param {HTMLElement} list
 * @param {Counts} byProvider
 */
function renderProviderList(list, byProvider) {
    const provs = Object.entries(byProvider)
        .sort(function ([a, na], [b, nb]) {
            if (a === 'Unknown') return 1
            if (b === 'Unknown') return -1
            const d = nb - na
            if (d !== 0) return d
            return compareNames(a, b)
        })
        .map(function ([prov]) {
            return prov
        })
    provs.forEach(function (prov) {
        const row = document.createElement('div')
        row.className = 'country-row'
        row.setAttribute('role', 'listitem')
        if (prov !== 'Unknown') row.setAttribute('data-prov', prov)
        const name = document.createElement('span')
        name.className = 'country-name'
        name.textContent = prov
        const count = document.createElement('span')
        count.className = 'country-count'
        count.textContent = String(byProvider[prov])
        row.appendChild(name)
        row.appendChild(count)
        list.appendChild(row)
    })
}

// renderCountryList builds one row per country, count descending,
// ties in the order of the names shown (compareNames), Unknown
// always last.
/**
 * @param {HTMLElement} list
 * @param {Counts} byCountry
 */
function renderCountryList(list, byCountry) {
    const codes = Object.entries(byCountry)
        .sort(function ([a, na], [b, nb]) {
            if (a === 'Unknown') return 1
            if (b === 'Unknown') return -1
            const d = nb - na
            if (d !== 0) return d
            return compareNames(countryName(a), countryName(b))
        })
        .map(function ([code]) {
            return code
        })
    codes.forEach(function (code) {
        const row = document.createElement('div')
        row.className = 'country-row'
        row.setAttribute('role', 'listitem')
        row.setAttribute('data-cc', code)

        const flag = document.createElement('span')
        flag.className = 'country-flag'
        flag.textContent = countryFlag(code)

        const name = document.createElement('span')
        name.className = 'country-name'
        name.textContent = countryName(code)

        const count = document.createElement('span')
        count.className = 'country-count'
        count.textContent = String(byCountry[code])

        row.appendChild(flag)
        row.appendChild(name)
        row.appendChild(count)
        list.appendChild(row)
    })
}

export function start() {
    const mapListToggle = $('map-list-toggle')
    mapListToggle.addEventListener('click', function (e) {
        const btn =
            e.target instanceof Element
                ? e.target.closest('button[data-mode]')
                : null
        if (!btn) return
        // The button's selector names the attribute.
        mapListMode = /** @type {string} */ (btn.getAttribute('data-mode'))
        const btns = mapListToggle.querySelectorAll('button')
        for (const b of btns) b.setAttribute('aria-pressed', String(b === btn))
        // The map clusters by the active mode, so it re-renders along
        // with the side list.
        renderMap(live.lastPeers)
    })
}
