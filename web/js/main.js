// The page's entry module: it starts the page's features in the page's
// order, each on its own, so one that fails to start leaves the others
// running.
import * as ages from './ages.js'
import * as alerts from './alerts.js'
import * as boot from './boot.js'
import * as copy from './copy.js'
import * as countryHighlight from './country-highlight.js'
import * as eta from './eta.js'
import * as headerLinks from './header-links.js'
import * as highlight from './highlight.js'
import * as live from './live.js'
import * as map from './map.js'
import * as networkState from './network-state.js'
import * as nodeLists from './node-lists.js'
import * as pointer from './pointer.js'
import * as popover from './popover.js'
import * as ranking from './ranking.js'
import * as rankingHelp from './ranking-help.js'
import * as recentBlocks from './recent-blocks.js'
import * as sections from './sections.js'
import { startEach } from './start.js'
import * as sync from './sync.js'
import * as timeline from './timeline.js'
import * as valpage from './valpage.js'

startEach([
    ['sections', sections.start],
    ['node-lists', nodeLists.start],
    ['popover', popover.start],
    ['recent-blocks', recentBlocks.start],
    ['map', map.start],
    ['pointer', pointer.start],
    ['country-highlight', countryHighlight.start],
    ['boot', boot.start],
    ['header-links', headerLinks.start],
    ['network-state', networkState.start],
    ['highlight', highlight.start],
    ['live', live.start],
    ['copy', copy.start],
    ['ages', ages.start],
    ['alerts', alerts.start],
    ['eta', eta.start],
    ['sync', sync.start],
    ['timeline', timeline.start],
    ['ranking', ranking.start],
    ['ranking-help', rankingHelp.start],
    ['valpage', valpage.start],
])
