self.addEventListener('push', function(event) {
    let data = { title: 'gnockpit alert', body: 'An alert has fired.' };
    if (event.data) {
        try { data = event.data.json(); } catch (_) {}
    }
    event.waitUntil(
        self.registration.showNotification(data.title, {
            body: data.body,
            icon: '/favicon.ico',
            badge: '/favicon.ico',
            tag: data.title,   // collapses duplicate notifications with the same title
            renotify: false,
        })
    );
});

self.addEventListener('notificationclick', function(event) {
    event.notification.close();
    event.waitUntil(
        clients.matchAll({ type: 'window', includeUncontrolled: true }).then(function(list) {
            for (const client of list) {
                if ('focus' in client) return client.focus();
            }
            if (clients.openWindow) return clients.openWindow('/');
        })
    );
});
