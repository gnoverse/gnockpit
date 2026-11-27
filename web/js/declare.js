// A validator page's Location section: a declared location's signed
// document, to copy or download; while its operator may declare, the
// form that builds the document declaring its location, or the one
// removing it, the gnokey commands that sign it, and the field the
// signed document is pasted in to submit it to POST /api/location. And
// the ⓘ of a declared location.

/** @import { LocationRecord, RemovalRecord } from './location-record.js' */
/** @import { StoredLocation } from '../types/location.js' */
/** @import { ValidatorLocation, ValidatorReport } from '../types/validator.js' */

import { compareNames } from './collate.js'
import { copyNotify } from './copy.js'
import { COUNTRY_CODES, countryName } from './countries.js'
import { countryOf, readBorders } from './country-borders.js'
import { $svg, mkEl, SVG_NS } from './dom.js'
import {
    listedProvider,
    locationMemo,
    ON_PREMISE,
    providerError,
    recordTime,
    signSnippet,
    tenth,
    unsignedDocument,
} from './location-record.js'
import { present } from './present.js'
import { boundedRead, errorOf, failureMessage } from './read.js'
import { live } from './state.js'
import { info, section } from './valpage-blocks.js'

// SUBMIT_BOUND_MS bounds a submission: one without an answer by then
// is given up, as one that failed.
const SUBMIT_BOUND_MS = 10000

// MAP_WIDTH and MAP_HEIGHT are the form's map's viewBox, the map
// card's: x = lon + 180, y = 90 - lat.
const MAP_WIDTH = 360
const MAP_HEIGHT = 180

/**
 * The location the form gives.
 * @typedef {object} FormLocation
 * @property {number} lat
 * @property {number} lon
 * @property {string} country
 * @property {string} provider
 */

/**
 * What the validator page does for its Location section's submissions.
 * @typedef {object} PageHooks
 * @property {() => void} clearNote empties the page's note of a
 *   declaration stored before, as a submission starts
 * @property {(said: string) => void} reread reads the report again once
 *   gnockpit stored a declaration, which said says ("declaration
 *   stored", or "removal stored"), while the page still shows the
 *   validator the section was built for
 */

/**
 * The form of a location, and what fills it as it opens.
 * @typedef {object} LocationForm
 * @property {HTMLFormElement} form
 * @property {HTMLInputElement} first its first field, Latitude
 * @property {(declared: ValidatorLocation | null) => void} fill starts
 *   the read of the country borders, which a page reads until a read
 *   succeeds, then keeps, offers the
 *   providers the dashboard's last message lists, and sets the fields to
 *   declared, a declared location, when given one
 */

// declaredInfo builds the ⓘ of a declared location: where its signing
// node is, and the signed document it comes from.
/** @returns {HTMLSpanElement} */
export function declaredInfo() {
    return info(
        'where its signing node is, as its operator declared it',
        'a document its operator signed with its operator key (gnokey), which gnockpit verified',
        ''
    )
}

// locationSection is the Location section of report rep's page, null
// unless its location is declared or its operator may declare it. While
// the operator may declare, the section offers it (see appendDeclaring),
// its submissions told to the page through page; a declared location's
// signed document, as gnockpit stores it, comes last, folded (see
// signedDocument).
/**
 * @param {ValidatorReport} rep
 * @param {PageHooks} page
 * @returns {HTMLDivElement | null}
 */
export function locationSection(rep, page) {
    const loc = rep.location
    const declared = loc && loc.source === 'declared' ? loc : null
    if (!declared && !rep.declarable) return null
    const sec = section('location', 'Location', declaredInfo())
    if (rep.declarable) appendDeclaring(sec, rep, declared, page)
    if (declared) sec.appendChild(signedDocument(declared, rep.operator))
    return sec
}

// signedDocument folds the signed document declared, operator's
// validator's declared location, under its toggle: opened, it shows the
// document, which ⧉ copy copies and ⤓ download saves as
// gnockpit-location-<operator>.json.
/**
 * @param {ValidatorLocation} declared
 * @param {string} operator
 * @returns {HTMLDetailsElement}
 */
function signedDocument(declared, operator) {
    const signed = present(declared.declared).signed
    const doc = mkEl('details', null, 'decl-doc')
    doc.appendChild(mkEl('summary', 'Signed document'))
    const acts = mkEl('div', null, 'decl-acts')
    acts.appendChild(
        copyButton('decl-copy', signed, 'copy the signed document')
    )
    acts.appendChild(
        mkEl('a', '⤓ download', 'hdr-btn decl-download', {
            href:
                'data:application/json;charset=utf-8,' +
                encodeURIComponent(signed),
            download: 'gnockpit-location-' + operator + '.json',
        })
    )
    doc.appendChild(acts)
    doc.appendChild(mkEl('pre', signed, 'decl-signed'))
    return doc
}

// appendDeclaring appends to sec what declares the location of report
// rep's validator: Declare its location; or, while declared is its
// declared location, Update and Remove. Declare its location and Update
// open the form, Update filling it with the declared location, focus
// its first field when a key pressed them, and close the signing steps
// shown; Continue, and Remove, which closes the form, show the steps
// that sign the document built in place of the ones shown, and say so
// in the section's status, which opening the form empties. A record is timed
// on the server's clock (see serverTime).
/**
 * @param {HTMLDivElement} sec
 * @param {ValidatorReport} rep
 * @param {ValidatorLocation | null} declared
 * @param {PageHooks} page
 */
function appendDeclaring(sec, rep, declared, page) {
    // The report's server time against the browser's clock, as the
    // report came: the section is built as it is shown.
    const reportOffset = Date.parse(rep.now) - Date.now()
    /** @type {HTMLDivElement | null} */
    let shown = null
    // The section's status: what Continue or Remove built,
    // or, an error, why no document shows when the record cannot be
    // timed.
    const status = mkEl('div', null, 'decl-status', { role: 'status' })
    // sign shows the steps that sign the document of the record recordAt
    // gives for the server's time, in place of the ones shown, and says
    // built in the status; without the server's time it shows none, and
    // says why. A removal's steps, which no form shows above, start with
    // the notice that only the operator can sign it (operatorNotice).
    /**
     * @param {(at: string) => LocationRecord | RemovalRecord} recordAt
     * @param {string} built
     */
    function sign(recordAt, built) {
        const at = serverTime(reportOffset)
        if (shown) shown.remove()
        shown = null
        if (at === null) {
            say(
                status,
                "could not build the document: the server's time is unknown; reload the page",
                true
            )
            return
        }
        const record = recordAt(at)
        shown = signingSteps(
            unsignedDocument(locationMemo(record), rep.operator),
            rep.operator,
            page
        )
        if ('clear' in record) shown.prepend(operatorNotice(rep.operator))
        status.after(shown)
        say(status, built, false)
    }
    const { form, first, fill } = locationForm(rep.operator, function (where) {
        sign(function (at) {
            return {
                chain: rep.chain,
                operator: rep.operator,
                lat: where.lat,
                lon: where.lon,
                country: where.country,
                provider: where.provider,
                at: at,
            }
        }, 'document built: sign it with the commands below')
    })
    const acts = mkEl('div', null, 'decl-acts')
    const open = mkEl(
        'button',
        declared ? 'Update' : 'Declare its location',
        'hdr-btn decl-open',
        { type: 'button' }
    )
    open.addEventListener('click', function (e) {
        fill(declared)
        if (shown) shown.remove()
        shown = null
        say(status, '', false)
        if (!form.isConnected) acts.after(form)
        // A key's click (detail 0) moves on to the first field; a
        // pointer's leaves the focus, so that a tap raises no phone
        // keyboard over the map.
        if (e.detail === 0) first.focus()
    })
    acts.appendChild(open)
    if (declared) {
        const remove = mkEl('button', 'Remove', 'hdr-btn decl-remove', {
            type: 'button',
        })
        remove.addEventListener('click', function () {
            form.remove()
            sign(function (at) {
                return {
                    chain: rep.chain,
                    operator: rep.operator,
                    clear: true,
                    at: at,
                }
            }, 'removal built: sign it with the commands below')
        })
        acts.appendChild(remove)
    }
    sec.appendChild(acts)
    sec.appendChild(status)
}

// serverTime is the current time as a record holds it, on the server's
// clock: the server refuses a record ahead of its clock, or not newer
// than the one it stores, whatever the browser's clock reads. The
// clock is the one the dashboard's messages give; before the first,
// reportOffset, the report's (server minus browser, in ms). It is null
// when neither gives a time: no message yet, and a report's now that is
// no time.
/**
 * @param {number} reportOffset
 * @returns {string | null}
 */
function serverTime(reportOffset) {
    const offset = live.serverTimeKnown ? live.serverTimeOffset : reportOffset
    if (!Number.isFinite(offset)) return null
    return recordTime(Date.now() + offset)
}

// listedProviders are the providers the form offers and matches Other…'s
// name against: the peers', the shown declarations' and on-premise, each
// once, in the order of their UTF-16 code units. A peer's spelling the
// server refuses (providerError: an AS organization's name may be one) is
// left out, as the server leaves it out, so that a name of its key keeps
// its own spelling. The server's list also holds the stored declarations'
// that do not show, in the order of their UTF-8 bytes, which differs only
// where a name holds a character past U+FFFF; the server matches the
// document's provider against its own list again, which decides the
// spelling it stores.
/** @returns {string[]} */
function listedProviders() {
    const listed = new Set([ON_PREMISE])
    for (const p of live.lastPeers ?? [])
        if (p.provider && providerError(p.provider) === '')
            listed.add(p.provider)
    for (const d of Object.values(live.declared))
        if (d.provider) listed.add(d.provider)
    return Array.from(listed).sort()
}

// locationForm builds the form of a location, under the notice that only
// operator, the validator's operator, can declare it (operatorNotice): the
// map, a click on which
// sets the coordinates to its point, and the country to the one it lies
// in once the country borders are read (countryOf), and a pin where they
// are; latitude and longitude, rounded to the 0.1° grid as they change,
// which leave the country as it is; the country, in name order; and the
// provider (see providerField). Once the country
// and the coordinates are given and the provider is one the server
// accepts (the browser checks the form), Continue passes the location to
// build.
/**
 * @param {string} operator
 * @param {(where: FormLocation) => void} build
 * @returns {LocationForm}
 */
function locationForm(operator, build) {
    const form = mkEl('form', null, 'decl-form')
    const lat = coordinateInput('lat', 90)
    const lon = coordinateInput('lon', 180)
    const country = countrySelect()
    const provider = providerField()
    const map = locationMap(function (pLat, pLon) {
        lat.value = String(tenth(pLat))
        lon.value = String(tenth(pLon))
        placePin()
        // A point at sea, or before the borders are read, leaves the
        // country as it is, as a country selected by hand.
        const code = countryOf(tenth(pLat), tenth(pLon))
        if (code !== null) country.value = code
    })
    // placePin puts the map's pin where the fields say, and hides it
    // while either says no number.
    function placePin() {
        map.pin(lat.valueAsNumber, lon.valueAsNumber)
    }
    lat.addEventListener('input', placePin)
    lon.addEventListener('input', placePin)
    lat.addEventListener('change', placePin)
    lon.addEventListener('change', placePin)

    const fields = mkEl('div', null, 'decl-fields')
    fields.appendChild(labelled('Latitude', lat))
    fields.appendChild(labelled('Longitude', lon))
    fields.appendChild(labelled('Country', country))
    fields.appendChild(labelled('Provider (optional)', provider.select))
    fields.appendChild(provider.named)
    const buildBtn = mkEl('button', 'Continue', 'hdr-btn decl-build', {
        type: 'submit',
    })
    fields.appendChild(buildBtn)
    form.appendChild(operatorNotice(operator))
    form.appendChild(map.svg)
    form.appendChild(fields)
    // A click on Continue, which Enter in a field makes too, comes before
    // the browser checks the form, each click offering the listed
    // providers again (see refresh) and checking the provider anew.
    buildBtn.addEventListener('click', provider.check)
    form.addEventListener('submit', function (e) {
        e.preventDefault()
        build({
            lat: tenth(lat.valueAsNumber),
            lon: tenth(lon.valueAsNumber),
            country: country.value,
            provider: provider.value(),
        })
    })
    return {
        form: form,
        first: lat,
        fill: function (declared) {
            // The read settles whatever its outcome: a failure leaves the
            // country to select by hand.
            readBorders()
            provider.refresh()
            if (!declared) return
            lat.value = String(present(declared.lat))
            lon.value = String(present(declared.lon))
            country.value = declared.country ?? ''
            provider.choose(declared.provider ?? '')
            placePin()
        },
    }
}

// coordinateInput is the field of coordinate name, a number within
// -limit and limit on the 0.1° grid, which a change rounds to the grid.
/**
 * @param {string} name
 * @param {number} limit
 * @returns {HTMLInputElement}
 */
function coordinateInput(name, limit) {
    const input = mkEl('input', null, null, {
        type: 'number',
        name: name,
        step: '0.1',
        min: String(-limit),
        max: String(limit),
        required: '',
    })
    input.addEventListener('change', function () {
        const x = input.valueAsNumber
        if (Number.isFinite(x)) input.value = String(tenth(x))
    })
    return input
}

// countrySelect is the country field: no country, then every code a
// declared location may name, in the order of their names.
/** @returns {HTMLSelectElement} */
function countrySelect() {
    const select = mkEl('select', null, null, { name: 'country', required: '' })
    select.appendChild(mkEl('option', '— country —', null, { value: '' }))
    COUNTRY_CODES.map((code) => ({ code: code, name: countryName(code) }))
        .sort((a, b) => compareNames(a.name, b.name))
        .forEach(function (c) {
            select.appendChild(mkEl('option', c.name, null, { value: c.code }))
        })
    return select
}

// OTHER_PROVIDER is the value of the provider select's Other… option.
const OTHER_PROVIDER = 'other'

// NO_PROVIDER_NAME is why Continue refuses Other… without a name: the
// server's words for a provider with no letter or digit.
const NO_PROVIDER_NAME = 'provider must hold a letter or a digit'

/**
 * The form's provider.
 * @typedef {object} ProviderField
 * @property {HTMLSelectElement} select
 * @property {HTMLLabelElement} named the field of Other…'s name, under its
 *   label, hidden unless Other… is chosen
 * @property {() => string[]} refresh offers the providers the dashboard's
 *   last message lists, keeping the choice, and returns them
 * @property {(name: string) => void} choose chooses provider name, '' for
 *   none: its option, else Other… with name as its name
 * @property {() => void} check offers the listed providers again (see
 *   refresh), gives Other…'s name its listed spelling, and has the
 *   browser refuse a name the server refuses, or none
 * @property {() => string} value the provider chosen, '' for none
 */

// providerField is the form's provider: a select of Not specified, On
// premise, the listed providers (see listedProviders) and Other…, which
// reveals a field for a name the select does not list. That name takes
// the listed spelling it matches once trimmed (listedProvider), and a
// name the server refuses, or none, is refused in the server's words;
// while another choice stands, the name is hidden and unused.
/** @returns {ProviderField} */
function providerField() {
    const select = mkEl('select', null, null, { name: 'provider' })
    const other = mkEl('option', 'Other…', null, { value: OTHER_PROVIDER })
    const name = mkEl('input', null, null, {
        name: 'provider-name',
        autocomplete: 'off',
    })
    const named = labelled('Provider name', name)
    named.hidden = true
    // showName shows the name's field while Other… is chosen.
    function showName() {
        named.hidden = !other.selected
    }
    select.addEventListener('change', showName)
    /** @param {string} p */
    function choose(p) {
        const option = Array.from(select.options).find(
            (o) => o !== other && o.value === p
        )
        if (option) {
            option.selected = true
        } else {
            other.selected = true
            name.value = p
        }
        showName()
    }
    function refresh() {
        const chosen = other.selected ? null : select.value
        const listed = listedProviders()
        select.replaceChildren(
            mkEl('option', 'Not specified', null, { value: '' }),
            mkEl('option', 'On premise', null, { value: ON_PREMISE }),
            ...listed
                .filter((p) => p !== ON_PREMISE)
                .map((p) => mkEl('option', p, null, { value: p })),
            other
        )
        if (chosen === null) {
            other.selected = true
        } else {
            choose(chosen)
        }
        return listed
    }
    return {
        select: select,
        named: named,
        refresh: refresh,
        choose: choose,
        check: function () {
            const listed = refresh()
            if (!other.selected) {
                name.setCustomValidity('')
                return
            }
            name.value = listedProvider(name.value.trim(), listed)
            name.setCustomValidity(
                name.value === '' ? NO_PROVIDER_NAME : providerError(name.value)
            )
        },
        value: function () {
            return other.selected ? name.value : select.value
        },
    }
}

// labelled is field under its label text.
/**
 * @param {string} text
 * @param {HTMLElement} field
 * @returns {HTMLLabelElement}
 */
function labelled(text, field) {
    const label = mkEl('label', text)
    label.appendChild(field)
    return label
}

// locationMap is the form's map: the map card's land, cloned, and a pin
// that pin places, or hides for a coordinate that is no number. A click
// calls point with the point's latitude and longitude.
/**
 * @param {(lat: number, lon: number) => void} point
 * @returns {{ svg: SVGSVGElement, pin: (lat: number, lon: number) => void }}
 */
function locationMap(point) {
    // The clone takes no id: an id names one element of the page.
    const land = /** @type {SVGElement} */ ($svg('world-land').cloneNode(true))
    land.removeAttribute('id')
    const svg = document.createElementNS(SVG_NS, 'svg')
    svg.setAttribute('class', 'decl-map')
    svg.setAttribute('viewBox', '0 0 ' + MAP_WIDTH + ' ' + MAP_HEIGHT)
    // The fields say what the map shows, and take what it gives.
    svg.setAttribute('aria-hidden', 'true')
    svg.appendChild(land)
    const pin = document.createElementNS(SVG_NS, 'circle')
    pin.setAttribute('class', 'decl-pin')
    pin.setAttribute('r', '2.5')
    pin.style.display = 'none'
    svg.appendChild(pin)
    svg.addEventListener('click', function (e) {
        // The map has no border or padding: its box is its viewBox's.
        const r = svg.getBoundingClientRect()
        const x = ((e.clientX - r.left) / r.width) * MAP_WIDTH
        const y = ((e.clientY - r.top) / r.height) * MAP_HEIGHT
        point(90 - y, x - 180)
    })
    return {
        svg: svg,
        pin: function (lat, lon) {
            const at = Number.isFinite(lat) && Number.isFinite(lon)
            pin.style.display = at ? '' : 'none'
            if (!at) return
            pin.setAttribute('cx', (lon + 180).toFixed(2))
            pin.setAttribute('cy', (90 - lat).toFixed(2))
        },
    }
}

// operatorNotice is the notice that only the validator's operator can
// declare its location, as gnockpit accepts only a document operator's
// key signs: a plain paragraph, "operator key" bold.
/**
 * @param {string} operator
 * @returns {HTMLParagraphElement}
 */
function operatorNotice(operator) {
    const notice = mkEl('p', null, 'decl-notice')
    notice.append(
        "Only this validator's operator can declare its location: gnockpit accepts the document only when the ",
        mkEl('strong', 'operator key'),
        ' of ' + operator + ' signs it.'
    )
    return notice
}

// signingSteps shows how to sign and submit doc, the unsigned document
// of operator's declaration: the gnokey commands that sign it, a POSIX
// shell's, which hold the document, and the field its signed copy is
// pasted in, which Submit posts (see submit).
/**
 * @param {string} doc
 * @param {string} operator
 * @param {PageHooks} page
 * @returns {HTMLDivElement}
 */
function signingSteps(doc, operator, page) {
    const snippet = signSnippet(doc, operator)
    const box = mkEl('div', null, 'decl-sign')
    // Step 1 names the key in bold: only the operator key's signature
    // is accepted.
    const step1 = mkEl('p', null, 'decl-step')
    step1.append(
        '1. Sign it with gnokey, in bash or zsh, on the machine whose gnokey holds the ',
        mkEl('strong', 'operator key'),
        ':'
    )
    box.appendChild(step1)
    box.appendChild(
        copyRow('decl-snippet-copy', snippet, 'copy the commands that sign it')
    )
    box.appendChild(mkEl('pre', snippet, 'decl-snippet'))
    const signed = mkEl('textarea', null, null, {
        name: 'signed',
        rows: '6',
        spellcheck: 'false',
        autocomplete: 'off',
    })
    const paste = mkEl(
        'label',
        '2. Paste the signed document gnokey printed:',
        'decl-step'
    )
    paste.appendChild(signed)
    box.appendChild(paste)
    const send = mkEl('button', 'Submit', 'hdr-btn decl-submit', {
        type: 'button',
    })
    const result = mkEl('div', null, 'decl-result', { role: 'status' })
    send.addEventListener('click', function () {
        submit(signed.value.trim(), send, result, page)
    })
    box.appendChild(send)
    box.appendChild(result)
    return box
}

// copyRow is a row of one ⧉ copy button, of class cls, copying text,
// named name (see copyButton).
/**
 * @param {string} cls
 * @param {string} text
 * @param {string} name
 * @returns {HTMLDivElement}
 */
function copyRow(cls, text, name) {
    const acts = mkEl('div', null, 'decl-acts')
    acts.appendChild(copyButton(cls, text, name))
    return acts
}

// copyButton is a ⧉ copy button, of class cls, copying text. Its
// accessible name is name, what it copies: a section may hold two.
/**
 * @param {string} cls
 * @param {string} text
 * @param {string} name
 * @returns {HTMLButtonElement}
 */
function copyButton(cls, text, name) {
    const b = mkEl('button', '⧉ copy', 'hdr-btn ' + cls, {
        type: 'button',
        'aria-label': name,
    })
    b.addEventListener('click', function (e) {
        copyNotify(text, e)
    })
    return b
}

// submit posts signed, the signed document, to POST /api/location,
// once: send, the Submit button, stays disabled until the answer. It
// empties the page's note of an earlier declaration stored as it
// starts. A stored declaration says so in result and has the page read
// the report again; a refusal shows its reason in result, an error, and
// so does a failure, and send takes the focus back when it had it. A
// submission unanswered within SUBMIT_BOUND_MS says that gnockpit may
// have stored it.
/**
 * @param {string} signed
 * @param {HTMLButtonElement} send
 * @param {HTMLDivElement} result
 * @param {PageHooks} page
 */
function submit(signed, send, result, page) {
    const focused = document.activeElement === send
    send.disabled = true
    say(result, '', false)
    page.clearNote()
    // refused shows why gnockpit stored nothing, and lets the
    // operator submit again; a disabled button loses the focus.
    /** @param {string} text */
    function refused(text) {
        say(result, text, true)
        send.disabled = false
        if (focused && document.activeElement === document.body) send.focus()
    }
    boundedRead(SUBMIT_BOUND_MS, function (signal) {
        return fetch('/api/location', {
            method: 'POST',
            body: signed,
            signal,
        }).then(
            /**
             * The declaration stored, its JSON unchecked (null for an
             * answer that is no JSON: stored all the same), or why not.
             * @param {Response} res
             * @returns {Promise<{ stored: StoredLocation | null } | { error: string }>}
             */
            function (res) {
                if (res.ok)
                    return res.json().then(
                        /** @param {StoredLocation} stored */
                        function (stored) {
                            return { stored: stored }
                        },
                        function () {
                            return { stored: null }
                        }
                    )
                // A refusal names its reason in JSON; any other
                // error answer, a proxy's error page, says its status.
                // A 503 is gnockpit not ready to decide (the chain id,
                // the validator set or a profile unread): the same
                // document may be stored shortly.
                const failed = 'could not submit: HTTP ' + res.status
                const later =
                    res.status === 503 ? ' — not stored; try again shortly' : ''
                return res.json().then(
                    /** @param {unknown} d */
                    function (d) {
                        return { error: (errorOf(d) ?? failed) + later }
                    },
                    function () {
                        return { error: failed + later }
                    }
                )
            }
        )
    }).then(
        function (r) {
            if ('error' in r) {
                refused(r.error)
                return
            }
            say(result, 'stored; reading the page again…', false)
            // A removal is a declaration too, the one word for an
            // answer the page cannot read.
            page.reread(
                r.stored && r.stored.removed
                    ? 'removal stored'
                    : 'declaration stored'
            )
        },
        /** @param {unknown} e */
        function (e) {
            // A submission given up may still be stored: gnockpit
            // finishes a write whatever its client does.
            if (e instanceof DOMException && e.name === 'TimeoutError') {
                refused(
                    'gnockpit did not answer within ' +
                        SUBMIT_BOUND_MS / 1000 +
                        ' s; the declaration may be stored: reopen this page to check'
                )
                return
            }
            refused('could not submit: ' + failureMessage(e))
        }
    )
}

// say writes text in status, a live region, marked as an error or not.
/**
 * @param {HTMLDivElement} status
 * @param {string} text
 * @param {boolean} error
 */
function say(status, text, error) {
    status.textContent = text
    status.classList.toggle('err', error)
}
