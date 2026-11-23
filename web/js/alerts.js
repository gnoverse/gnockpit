// Push alerts: the Alerts dialog, where the viewer picks what this
// device is alerted of, and the browser's push subscription behind it.

/** @import { DeviceIDAnswer, EntitiesResponse, VapidKeyAnswer } from '../types/push.js' */

import { $, mkEl } from './dom.js'
import { keepFocusIn } from './focus.js'
import { validatorName } from './names.js'
import { boundedRead, failureMessage } from './read.js'
import { live } from './state.js'
import { storageObject, storageRemove, storageSet } from './storage.js'

/** @type {EntitiesResponse | null} */
let _pushEntities = null
// pushReady settles once initPush knows whether push works: start
// runs initPush.
/** @type {Promise<void> | null} */
let pushReady = null

// whenPushReady is pushReady, for another module to await: null
// until start runs initPush.
/** @returns {Promise<void> | null} */
export function whenPushReady() {
    return pushReady
}

/** @returns {Promise<void>} */
async function initPush() {
    if (
        !window.isSecureContext ||
        !('serviceWorker' in navigator) ||
        !('PushManager' in window)
    )
        return
    try {
        await navigator.serviceWorker.register('/service-worker.js')
        const key = await boundedRead(answerTimeoutMs, async function (signal) {
            const r = await fetch('/api/push/vapid-key', {
                signal,
            })
            if (!r.ok) return null
            /** @type {VapidKeyAnswer} */
            const d = await r.json()
            return d.key
        })
        // null: the server has no push.
        if (key === null) return
        live.vapidKey = key
        $('push-btn').style.display = ''
    } catch (e) {
        console.warn('push init failed:', e)
    }
}

/** @returns {Promise<void>} */
async function openAlertsModal() {
    const modal = $('alerts-modal')
    modal.classList.add('open')
    $('alerts-close').focus()
    showAlertsError('')
    // Without the server's list, the form offers the chain alerts
    // alone, on the page's defaults, and says why.
    try {
        _pushEntities = await boundedRead(
            answerTimeoutMs,
            async function (signal) {
                const r = await fetch('/api/push/entities', {
                    signal,
                })
                if (!r.ok) throw new Error('HTTP ' + r.status)
                /** @type {EntitiesResponse} */
                const d = await r.json()
                return d
            }
        )
    } catch (e) {
        _pushEntities = null
        showAlertsError(
            'Could not read the validators (' + failureMessage(e) + ').'
        )
    }
    renderAlertsForm()
    $('push-test-btn').style.display = live.pushSubID ? '' : 'none'
    const prefs = storageObject('gnockpitAlertPrefs')
    Object.entries(prefs).forEach(([id, v]) => {
        // The form's controls saveAlerts stores: inputs, and the halt
        // lead's unit, a select.
        const el = /** @type {HTMLInputElement | HTMLSelectElement | null} */ (
            document.getElementById(id)
        )
        if (!el) return
        if (el.type === 'checkbox') el.checked = Boolean(v)
        else el.value = String(v)
    })
    syncValAllState()
}

// closeAlertsModal closes the open dialog and gives the keyboard
// back to Alerts; a save ending after the dialog closed leaves
// the keyboard where the user took it.
function closeAlertsModal() {
    const modal = $('alerts-modal')
    if (!modal.classList.contains('open')) return
    modal.classList.remove('open')
    $('push-btn').focus()
}

function renderAlertsForm() {
    const perm = Notification.permission
    $('alerts-perm-status').textContent =
        perm === 'granted'
            ? 'Notifications are enabled in this browser.'
            : perm === 'denied'
              ? 'Notifications are blocked. Enable them in browser settings.'
              : 'You will be asked for permission when saving.'
    const entities = _pushEntities || {
        validators: [],
        chain_stuck_secs: 30,
        max_missed_in_a_row: 10,
    }
    const chainStuckSecs = entities.chain_stuck_secs || 30
    const maxMissedInARow = entities.max_missed_in_a_row || 10
    const validators = entities.validators || []

    const chainStuck = alertRow(
        'alerts-main',
        'chain_stuck',
        'chain_stuck',
        '',
        alertLabel(
            'chk-chain_stuck',
            'Chain stuck',
            'for ' + chainStuckSecs + 's'
        ),
        true
    )

    // Chain events: once per event, no recovery.
    /**
     * @param {string} type
     * @param {string} label
     * @param {string} hint
     */
    const chainRow = (type, label, hint) =>
        alertRow(
            'alerts-row',
            type,
            type,
            '',
            alertLabel('chk-' + type, label, hint),
            false
        )
    const halt = chainRow(
        'halt_approaching',
        'Coordinated halt approaching',
        ''
    )
    halt.classList.add('alerts-wrap')
    const unit = mkEl('select', null, null, {
        id: 'lead-halt_approaching-unit',
        'aria-label': 'lead unit',
    })
    unit.append(
        mkEl('option', 'minutes', null, { value: 'm' }),
        mkEl('option', 'blocks', null, { value: 'blocks' })
    )
    const lead = mkEl('span', null, 'alerts-lead')
    lead.append(
        mkEl('input', null, null, {
            type: 'number',
            id: 'lead-halt_approaching-n',
            min: '1',
            value: '30',
            'aria-label': 'lead',
        }),
        unit,
        mkEl('span', 'before', 'alerts-hint')
    )
    halt.append(lead)
    const chain = alertSection('Chain', 'once per event, no recovery', [
        chainRow(
            'valset_changed',
            'Validator set changes',
            'joins, leaves, power'
        ),
        chainRow('halt_scheduled', 'New coordinated halt', ''),
        halt,
    ])

    // Validator missing blocks: an "all" row, then one row per
    // validator, labelled by its moniker (without the " (us)" the
    // server's name of the source's own carries) and address. The
    // server names a validator without a moniker by its address.
    const valRows = validators.map((v) => {
        const entity = v.val_address || ''
        const moniker = validatorName(v.moniker).name
        const text =
            [
                moniker !== entity ? moniker : '',
                v.val_address ? v.val_address.slice(0, 14) + '\u2026' : null,
            ]
                .filter(Boolean)
                .join(' \u00b7 ') || entity
        const key =
            'validator_missing_blocks__' +
            entity.replace(/[^a-zA-Z0-9_-]/g, '_')
        const label = mkEl('label', text, 'alerts-label alerts-name', {
            for: 'chk-' + key,
            'data-popover': text,
        })
        return alertRow(
            'alerts-row',
            key,
            'validator_missing_blocks',
            entity,
            label,
            true
        )
    })
    if (valRows.length > 0) {
        const all = mkEl('div', null, 'alerts-row alerts-all')
        all.append(
            mkEl('input', null, null, {
                type: 'checkbox',
                id: 'chk-val-all',
            }),
            mkEl('label', 'all', 'alerts-label', {
                for: 'chk-val-all',
            }),
            mkEl('input', null, null, {
                type: 'checkbox',
                id: 'rec-val-all',
            }),
            mkEl('label', 'all', 'alerts-rec', {
                for: 'rec-val-all',
            })
        )
        valRows.unshift(all)
    }
    const vals = alertSection(
        'Validator missing blocks',
        maxMissedInARow + ' in a row',
        valRows
    )

    $('alerts-form').replaceChildren(chainStuck, chain, vals)
}

// alertLabel labels box id with text, then a hint, if any.
/**
 * @param {string} id
 * @param {string} text
 * @param {string} hint
 * @returns {HTMLLabelElement}
 */
function alertLabel(id, text, hint) {
    const label = mkEl('label', text, 'alerts-label', { for: id })
    if (hint) label.append(mkEl('span', hint, 'alerts-hint'))
    return label
}

// alertRow is the row of an alert of type on entity: its box
// chk-<key> and label, then, with recovery, the box rec-<key> of
// its recovery and its label.
/**
 * @param {string} cls
 * @param {string} key
 * @param {string} type
 * @param {string} entity
 * @param {HTMLElement} label
 * @param {boolean} recovery
 * @returns {HTMLDivElement}
 */
function alertRow(cls, key, type, entity, label, recovery) {
    const row = mkEl('div', null, cls)
    const box = mkEl('input', null, null, {
        type: 'checkbox',
        id: 'chk-' + key,
        'data-type': type,
        'data-entity': entity,
    })
    row.append(box, label)
    if (!recovery) return row
    const recPop = 'Also notify on recovery'
    row.append(
        mkEl('input', null, null, {
            type: 'checkbox',
            id: 'rec-' + key,
            'data-rec-type': type,
            'data-rec-entity': entity,
            'data-popover': recPop,
        }),
        mkEl('label', 'recovery', 'alerts-rec', {
            for: 'rec-' + key,
            'data-popover': recPop,
        })
    )
    return row
}

// alertSection is a titled group of rows, its title followed by a
// hint.
/**
 * @param {string} title
 * @param {string} hint
 * @param {HTMLElement[]} rows
 * @returns {HTMLDivElement}
 */
function alertSection(title, hint, rows) {
    const section = mkEl('div', null, 'alerts-section')
    const head = mkEl('div', title, 'alerts-section-title')
    head.append(mkEl('span', hint, 'alerts-hint'))
    section.append(head, ...rows)
    return section
}

// onAlertsFormChange applies the check rules to the box that
// changed.
/** @param {HTMLInputElement} box */
function onAlertsFormChange(box) {
    if (box.id === 'chk-val-all') onValAllCheck(box, false)
    else if (box.id === 'rec-val-all') onValAllCheck(box, true)
    else if (box.dataset.recType !== undefined) onRecoveryCheck(box)
    else if (box.dataset.type !== undefined) onAlertCheck(box)
}

// showAlertsError says msg in the dialog; '' says nothing.
/** @param {string} msg */
function showAlertsError(msg) {
    $('alerts-error').textContent = msg
}

// recoveryBoxOf and alertBoxOf pair an alert's box chk-<key>
// with its recovery's rec-<key>: by id, whatever the entity holds.
// alertRow builds both.
/**
 * @param {HTMLInputElement} box
 * @returns {HTMLInputElement | null}
 */
function recoveryBoxOf(box) {
    return /** @type {HTMLInputElement | null} */ (
        document.getElementById('rec-' + box.id.slice(4))
    )
}
/**
 * @param {HTMLInputElement} rec
 * @returns {HTMLInputElement | null}
 */
function alertBoxOf(rec) {
    return /** @type {HTMLInputElement | null} */ (
        document.getElementById('chk-' + rec.id.slice(4))
    )
}

/** @param {HTMLInputElement} cb */
function onAlertCheck(cb) {
    // Unchecking an alert also unchecks its recovery (no recovery without the alert).
    if (!cb.checked) {
        const rec = recoveryBoxOf(cb)
        if (rec) rec.checked = false
    }
    syncValAllState()
}

// onRecoveryCheck checks the alert of a checked recovery; the
// "all" boxes follow either way.
/** @param {HTMLInputElement} cb */
function onRecoveryCheck(cb) {
    if (cb.checked) {
        const alertCb = alertBoxOf(cb)
        if (alertCb) alertCb.checked = true
    }
    syncValAllState()
}

// validatorBoxes are the form's boxes of the validators' alerts,
// or with recovery of their recoveries; all of them inputs: both
// selectors name input.
/**
 * @param {boolean} recovery
 * @returns {NodeListOf<HTMLInputElement>}
 */
function validatorBoxes(recovery) {
    return /** @type {NodeListOf<HTMLInputElement>} */ (
        document.querySelectorAll(
            recovery
                ? '#alerts-form input[data-rec-type="validator_missing_blocks"]'
                : '#alerts-form input[data-type="validator_missing_blocks"][data-entity]'
        )
    )
}

// onValAllCheck sets every validator's box, or with isRec every
// recovery's, as the "all" box cb is.
/**
 * @param {HTMLInputElement} cb
 * @param {boolean} isRec
 */
function onValAllCheck(cb, isRec) {
    validatorBoxes(isRec).forEach((el) => {
        el.checked = cb.checked
    })
    if (isRec && cb.checked) {
        // Also check all alert checkboxes when checking all recovery.
        validatorBoxes(false).forEach((el) => {
            el.checked = true
        })
        // renderAlertsForm builds the "all" boxes, both in one row.
        const allCb = /** @type {HTMLInputElement} */ ($('chk-val-all'))
        allCb.checked = true
    }
    if (!isRec && !cb.checked) {
        // Unchecking all alerts also unchecks all recovery.
        validatorBoxes(true).forEach((el) => {
            el.checked = false
        })
        // renderAlertsForm builds the "all" boxes.
        const recAll = /** @type {HTMLInputElement | null} */ (
            document.getElementById('rec-val-all')
        )
        if (recAll) recAll.checked = false
    }
}

function syncValAllState() {
    // renderAlertsForm builds the "all" boxes, with validators only.
    const allCb = /** @type {HTMLInputElement | null} */ (
        document.getElementById('chk-val-all')
    )
    const recAllCb = /** @type {HTMLInputElement | null} */ (
        document.getElementById('rec-val-all')
    )
    if (!allCb) return
    const alertCbs = [...validatorBoxes(false)]
    const recCbs = [...validatorBoxes(true)]
    if (alertCbs.length) allCb.checked = alertCbs.every((c) => c.checked)
    if (recCbs.length && recAllCb)
        recAllCb.checked = recCbs.every((c) => c.checked)
}

// urlBase64ToUint8Array reads the bytes of URL-safe base64 text.
/**
 * @param {string} base64
 * @returns {Uint8Array<ArrayBuffer>}
 */
function urlBase64ToUint8Array(base64) {
    const pad = '='.repeat((4 - (base64.length % 4)) % 4)
    const b64 = (base64 + pad).replace(/-/g, '+').replace(/_/g, '/')
    const raw = atob(b64)
    return Uint8Array.from(Array.from(raw).map((c) => c.charCodeAt(0)))
}

// swReadyTimeoutMs bounds the wait for the service worker, which
// never gets ready where registration silently did nothing.
const swReadyTimeoutMs = 10000

// answerTimeoutMs bounds the wait for the server's answer to a
// save, or to a reminder's read or removal.
export const answerTimeoutMs = 10000

// saveFailure is what a dialog says of a save that failed with e,
// as its deadline reports it.
/**
 * @param {unknown} e
 * @returns {string}
 */
export function saveFailure(e) {
    return e instanceof DOMException && e.name === 'TimeoutError'
        ? 'The server did not answer in time. Try again.'
        : 'Failed to save. See console for details.'
}

// serviceWorkerReady is navigator.serviceWorker.ready, or an
// error once swReadyTimeoutMs has passed.
/** @returns {Promise<ServiceWorkerRegistration>} */
async function serviceWorkerReady() {
    let timer = 0
    /** @type {Promise<never>} */
    const late = new Promise((_, reject) => {
        const msg = `service worker not ready after ${swReadyTimeoutMs / 1000} s`
        timer = setTimeout(() => reject(new Error(msg)), swReadyTimeoutMs)
    })
    try {
        return await Promise.race([navigator.serviceWorker.ready, late])
    } finally {
        clearTimeout(timer)
    }
}

// browserPushSub returns this browser's push subscription,
// subscribing it when create is set.
/**
 * @overload
 * @param {true} create
 * @returns {Promise<PushSubscription>}
 */
/**
 * @overload
 * @param {boolean} create
 * @returns {Promise<PushSubscription | null>}
 */
/**
 * @param {boolean} create
 * @returns {Promise<PushSubscription | null>}
 */
export async function browserPushSub(create) {
    const sw = await serviceWorkerReady()
    let sub = await sw.pushManager.getSubscription()
    if (!sub && create) {
        // Alerts and the reminder show once initPush has the key.
        const key = /** @type {string} */ (live.vapidKey)
        sub = await sw.pushManager.subscribe({
            userVisibleOnly: true,
            applicationServerKey: urlBase64ToUint8Array(key),
        })
    }
    return sub
}

// deviceKeys is what the server knows this device by.
/**
 * @param {PushSubscription} sub
 * @returns {{ endpoint: string, p256dh: string, auth: string }}
 */
export function deviceKeys(sub) {
    // A push subscription has both keys.
    /** @param {PushEncryptionKeyName} k */
    const b64 = (k) =>
        btoa(
            String.fromCharCode(
                ...new Uint8Array(/** @type {ArrayBuffer} */ (sub.getKey(k)))
            )
        )
    return {
        endpoint: sub.endpoint,
        p256dh: b64('p256dh'),
        auth: b64('auth'),
    }
}

// keepDevice records the device id the server answered; "" means
// it forgot the device, which had nothing left: the browser's push
// subscription ends too.
/**
 * @param {string} id
 * @returns {Promise<void>}
 */
export async function keepDevice(id) {
    if (id) {
        live.pushSubID = id
        storageSet('gnockpitPushSubID', id)
        return
    }
    forgetDevice()
    const sub = await browserPushSub(false)
    if (sub) await sub.unsubscribe()
}

// forgetDevice drops the stored id of a device the server does
// not hold.
function forgetDevice() {
    live.pushSubID = null
    storageRemove('gnockpitPushSubID')
}

// haltLead reads the approaching-halt lead: "30m" or "100 blocks";
// null when the form holds none from 1.
/** @returns {string | null} */
function haltLead() {
    // renderAlertsForm builds the lead's input and its unit's select.
    const n = parseInt(
        /** @type {HTMLInputElement} */ ($('lead-halt_approaching-n')).value,
        10
    )
    if (!(n >= 1)) return null
    const unit = /** @type {HTMLSelectElement} */ (
        $('lead-halt_approaching-unit')
    ).value
    return unit === 'blocks' ? n + ' blocks' : n + 'm'
}

// pushSaveSettings saves the menu; a failure is said in it, the
// menu staying open. Save is off while the save runs.
/** @returns {Promise<void>} */
async function pushSaveSettings() {
    const save = /** @type {HTMLButtonElement} */ ($('push-save-btn'))
    save.disabled = true
    save.textContent = 'Saving\u2026'
    try {
        await saveAlerts()
    } catch (e) {
        console.warn('save alerts:', e)
        showAlertsError(saveFailure(e))
    } finally {
        save.disabled = false
        save.textContent = 'Save'
        // Disabled, Save lost the keyboard: it gets it back while
        // the dialog stays open.
        if ($('alerts-modal').classList.contains('open')) save.focus()
    }
}

/**
 * An alert of the device as a save sends it, push.SubscriptionAlert
 * (web/push/types.go): a halt lead the form cannot read is null, which
 * the save refuses.
 * @typedef {object} SavedAlert
 * @property {string} alert_type
 * @property {string} entity_id
 * @property {boolean} recovery_notif
 * @property {string} subscription_id
 * @property {string | null} [lead]
 */

// saveAlerts saves the alerts the form checks for this device,
// subscribing the browser to push when any is.
/** @returns {Promise<void>} */
async function saveAlerts() {
    // Without the server's list of validators the form shows none
    // of the device's validator alerts: the save keeps them as the
    // server holds them, and the page keeps their boxes' state.
    const keep = _pushEntities ? [] : ['validator_missing_blocks']
    // With the list, the save names the validators the form shows:
    // the server keeps the device's alerts on any other, such as a
    // validator that left the set. Without it, shown is undefined,
    // which the request's JSON leaves out.
    const shown = _pushEntities
        ? // validatorBoxes' selector names data-entity.
          Array.from(
              validatorBoxes(false),
              (cb) => /** @type {string} */ (cb.dataset.entity)
          )
        : undefined
    /** @type {SavedAlert[]} */
    const alerts = []
    const boxes = /** @type {NodeListOf<HTMLInputElement>} */ (
        document.querySelectorAll('#alerts-form input[data-type]')
    )
    boxes.forEach((cb) => {
        if (!cb.checked) return
        // The boxes' selector names data-type; alertRow gives each
        // box its data-entity too.
        const type = /** @type {string} */ (cb.dataset.type)
        const rec = recoveryBoxOf(cb)
        /** @type {SavedAlert} */
        const a = {
            alert_type: type,
            entity_id: /** @type {string} */ (cb.dataset.entity),
            recovery_notif: !!(rec && rec.checked),
            subscription_id: '',
        }
        if (type === 'halt_approaching') a.lead = haltLead()
        alerts.push(a)
    })
    if (alerts.some((a) => a.lead === null)) {
        showAlertsError('Enter how far ahead of the halt, 1 or more.')
        return
    }

    let pushSub
    if (alerts.length > 0) {
        if (Notification.permission !== 'granted') {
            const p = await Notification.requestPermission()
            if (p !== 'granted') {
                showAlertsError('Notification permission denied.')
                return
            }
        }
        pushSub = await browserPushSub(true)
    } else {
        // Nothing checked: the device keeps only its reminder,
        // if any.
        pushSub = await browserPushSub(false)
    }
    if (pushSub) {
        // The device keeps its id across saves: what was sent to
        // it and its reminder stay. The server's answer is waited
        // for answerTimeoutMs at most; a refused save reads as
        // no id.
        const id = await boundedRead(answerTimeoutMs, async function (signal) {
            const r = await fetch('/api/push/subscribe', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({
                    ...deviceKeys(pushSub),
                    alerts,
                    keep,
                    shown,
                }),
                signal,
            })
            if (!r.ok) {
                console.warn('save alerts:', r.status, await r.text())
                return null
            }
            /** @type {DeviceIDAnswer} */
            const d = await r.json()
            return d.id
        })
        if (id === null) {
            showAlertsError('Failed to save. See console for details.')
            return
        }
        await keepDevice(id)
    }
    // The checked boxes the form does not show keep their stored
    // state, as the server keeps their alerts: every validator's
    // while the list is unread, and those of the validators that left
    // the set. An unchecked one is forgotten, so the stored form does
    // not grow with each validator that leaves.
    /** @type {Record<string, unknown>} */
    const prefs = Object.fromEntries(
        Object.entries(storageObject('gnockpitAlertPrefs')).filter(
            ([, v]) => v !== false
        )
    )
    const controls =
        /** @type {NodeListOf<HTMLInputElement | HTMLSelectElement>} */ (
            document.querySelectorAll(
                '#alerts-form input[data-type], #alerts-form input[data-rec-type], #lead-halt_approaching-n, #lead-halt_approaching-unit'
            )
        )
    controls.forEach((el) => {
        prefs[el.id] = el.type === 'checkbox' ? el.checked : el.value
    })
    storageSet('gnockpitAlertPrefs', JSON.stringify(prefs))
    closeAlertsModal()
}

/** @returns {Promise<void>} */
async function pushTestNotification() {
    if (!live.pushSubID) return
    const btn = /** @type {HTMLButtonElement} */ ($('push-test-btn'))
    btn.disabled = true
    btn.textContent = 'Sending\u2026'
    showAlertsError('')
    try {
        // The server's answer is waited for answerTimeoutMs at
        // most.
        const r = await boundedRead(answerTimeoutMs, (signal) =>
            fetch('/api/push/test', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ id: live.pushSubID }),
                signal,
            })
        )
        if (!r.ok) {
            const failed = 'Test notification failed (' + r.status + ').'
            btn.textContent = 'Test'
            btn.disabled = false
            // 404: the server does not hold this device; a save
            // with alerts registers it again.
            if (r.status === 404) {
                showAlertsError(failed + ' Save to register this device again.')
                forgetDevice()
                btn.style.display = 'none'
                $('push-save-btn').focus()
            } else {
                showAlertsError(failed)
            }
            return
        }
        btn.textContent = 'Sent!'
        setTimeout(function () {
            btn.textContent = 'Test'
            btn.disabled = false
        }, 2000)
    } catch (e) {
        showAlertsError('Test notification failed. See console.')
        console.error('push test failed:', e)
        btn.textContent = 'Test'
        btn.disabled = false
    }
}

export function start() {
    // The dialog's controls are handled from the document:
    // renderAlertsForm rebuilds the form's inputs at each opening,
    // and a listener there outlives them.
    document.addEventListener('click', (e) => {
        if (!(e.target instanceof Element)) return
        if (e.target.closest('#push-btn')) {
            openAlertsModal().catch((err) => console.error('open alerts:', err))
        } else if (
            e.target.id === 'alerts-modal' ||
            e.target.closest('#alerts-close')
        ) {
            closeAlertsModal()
        } else if (e.target.closest('#push-test-btn')) {
            pushTestNotification().catch((err) =>
                console.error('push test failed:', err)
            )
        } else if (e.target.closest('#push-save-btn')) {
            pushSaveSettings().catch((err) =>
                console.error('save alerts:', err)
            )
        }
    })
    document.addEventListener('change', (e) => {
        if (
            e.target instanceof HTMLInputElement &&
            e.target.closest('#alerts-form')
        )
            onAlertsFormChange(e.target)
    })
    // The open dialog closes on Escape and keeps Tab inside it.
    document.addEventListener('keydown', (e) => {
        const modal = $('alerts-modal')
        if (!modal.classList.contains('open')) return
        if (e.key === 'Escape') closeAlertsModal()
        if (e.key === 'Tab') keepFocusIn(modal, e)
    })

    pushReady = initPush()
}
