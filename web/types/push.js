// The /api/push answers, as their JSON tags write them: an omitempty field
// is optional, and a slice the server can leave nil is nullable. An answer
// the handler writes from a map is named after what it holds.

/**
 * The /api/push/vapid-key answer, handlePushVAPIDKey's map (web/server.go):
 * the server's VAPID public key.
 * @typedef {object} VapidKeyAnswer
 * @property {string} key
 */

/**
 * push.EntitiesResponse (web/push/types.go): the /api/push/entities
 * answer.
 * @typedef {object} EntitiesResponse
 * @property {Entity[] | null} validators
 * @property {number} chain_stuck_secs
 * @property {number} max_missed_in_a_row
 */

/**
 * push.Entity (web/push/types.go): a validator an alert can watch.
 * @typedef {object} Entity
 * @property {string} [val_address]
 * @property {string} [moniker]
 */

/**
 * The answer of a save to /api/push/subscribe, handlePushSubscribe's map
 * (web/server.go), or to /api/push/reminder, putReminder's
 * (web/reminder.go): the device's id.
 * @typedef {object} DeviceIDAnswer
 * @property {string} id
 */

/**
 * reminderView (web/reminder.go): a device's reminder, the
 * /api/push/reminder read's answer.
 * @typedef {object} ReminderView
 * @property {number} height
 * @property {string} since
 * @property {string} label
 * @property {string} lead
 */

/**
 * The answer of a removal from /api/push/reminder, handlePushReminder's map
 * (web/reminder.go): whether the device stays, with alerts left.
 * @typedef {object} ReminderRemovalAnswer
 * @property {boolean} device_kept
 */

export {}
