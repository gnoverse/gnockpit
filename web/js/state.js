// The live state the dashboard's features share: what the last /ws
// messages and /api/boot said, and this device's push subscription. One
// object, which every feature reads and writes as live.<name>, so no module
// writes a binding it imports, and the modules import one another in no
// cycle.

/** @import { BlockInfo, Peer, Status, VotesReport } from '../types/node.js' */

import { storageGet } from './storage.js'

/**
 * @typedef {object} Live
 * @property {VotesReport | null} lastVotes the votes of the last message; null before
 *   one, and while the sources have none
 * @property {Peer[] | null} lastPeers the peers of the last message, in name order;
 *   null before one, and while the sources have none
 * @property {Status | null} lastStatus the status of the last message that had one;
 *   null before
 * @property {boolean} hideSources set from the server's --hide-sources flag
 * @property {string} chainNetwork the chain id of the last status
 * @property {string | null} chainNameOverride the server's --chain-name
 *   flag, '' when unset; null until /api/boot first answers
 * @property {BlockInfo[] | null} lastRecentBlocks the recent blocks of the last message
 *   that had any; null before
 * @property {boolean} dataSeen whether a snapshot or an update has come:
 *   until then the tables keep their loading rows
 * @property {number} serverTimeOffset server UTC - local UTC in ms
 * @property {number} genesisTimeMs the chain's genesis time, in ms since
 *   the epoch; 0 until the server's checks give it
 * @property {string} genesisTimeStr the chain's genesis time as the server
 *   writes it; '' until its checks give it
 * @property {string | null} pushSubID the id the server gave this device,
 *   as stored at gnockpitPushSubID; null without one
 * @property {string | null} vapidKey the server's push key; null until the
 *   alerts' start reads it, and while the server has no push
 */

/** @type {Live} */
export const live = {
    lastVotes: null,
    lastPeers: null,
    lastStatus: null,
    hideSources: false,
    chainNetwork: '',
    chainNameOverride: null,
    lastRecentBlocks: null,
    dataSeen: false,
    serverTimeOffset: 0,
    genesisTimeMs: 0,
    genesisTimeStr: '',
    pushSubID: storageGet('gnockpitPushSubID') || null,
    vapidKey: null,
}
