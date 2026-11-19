self.addEventListener('push', function(event) {
    let data = { title: 'gnockpit alert', body: 'An alert has fired.' };
    if (event.data) {
        try { data = event.data.json(); } catch (_) {}
    }
    event.waitUntil(
        self.registration.showNotification(data.title, {
            body: data.body,
            data: { url: data.url || '/' },
            icon: '/icon-192.png',
            badge: '/icon-32.png',
            tag: data.title + '\x00' + data.body,   // unique per alert+entity so simultaneous alerts don't collapse
            renotify: false,
        })
    );
});

// A tap opens the page the push names (a reminder's card), in a dashboard
// window already open when there is one.
self.addEventListener('notificationclick', function(event) {
    event.notification.close();
    const url = (event.notification.data && event.notification.data.url) || '/';
    event.waitUntil(
        clients.matchAll({ type: 'window', includeUncontrolled: true }).then(function(list) {
            for (const client of list) {
                if ('focus' in client) {
                    if (url !== '/' && 'navigate' in client) return client.navigate(url).then(function(c) { return (c || client).focus(); });
                    return client.focus();
                }
            }
            if (clients.openWindow) return clients.openWindow(url);
        })
    );
});
