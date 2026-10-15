// Reads this Mac's calendars through EventKit, for osascript -l JavaScript: every account in System Settings →
// Internet Accounts, a Google one included, with repeating meetings already expanded. argv: how many seconds ahead
// to look, then "status" to only say whether there's access, without asking for it. macOS grants access to the app
// Oriel runs as, so the answer names that app with the access state and the events.
ObjC.import('EventKit');
ObjC.import('AppKit');
function hostApp() {
  const id = $.NSProcessInfo.processInfo.environment.objectForKey('__CFBundleIdentifier');
  if (id.isNil()) return '';
  const url = $.NSWorkspace.sharedWorkspace.URLForApplicationWithBundleIdentifier(id);
  return url.isNil() ? '' : $.NSFileManager.defaultManager.displayNameAtPath(url.path).js.replace(/\.app$/, '');
}
function run(argv) {
  const store = $.EKEventStore.alloc.init;
  const status = () => Number($.EKEventStore.authorizationStatusForEntityType($.EKEntityTypeEvent));
  if (argv[1] !== 'status' && (status() === 0 || status() === 4)) {
    let done = false;
    const cb = () => { done = true; };
    if (store.respondsToSelector('requestFullAccessToEventsWithCompletion:')) store.requestFullAccessToEventsWithCompletion(cb);
    else store.requestAccessToEntityTypeCompletion($.EKEntityTypeEvent, cb);
    const until = Date.now() + 50000;
    while (!done && Date.now() < until) {
      $.NSRunLoop.currentRunLoop.runModeBeforeDate($.NSDefaultRunLoopMode, $.NSDate.dateWithTimeIntervalSinceNow(0.2));
    }
  }
  const names = { 0: 'undecided', 1: 'restricted', 2: 'denied', 3: 'ok', 4: 'writeonly' };
  const st = names[status()] || 'denied';
  if (st !== 'ok' || argv[1] === 'status') return JSON.stringify({ status: st, app: hostApp() });
  const from = $.NSDate.dateWithTimeIntervalSinceNow(-86400);
  const to = $.NSDate.dateWithTimeIntervalSinceNow(Number(argv[0]));
  const found = store.eventsMatchingPredicate(store.predicateForEventsWithStartDateEndDateCalendars(from, to, $()));
  const out = [];
  for (let i = 0; i < found.count; i++) {
    const e = found.objectAtIndex(i);
    // EKEventStatusCanceled is 3.
    if (e.allDay || Number(e.status) === 3) continue;
    let declined = false;
    const people = e.attendees.isNil() ? 0 : Number(e.attendees.count);
    for (let j = 0; j < people; j++) {
      const p = e.attendees.objectAtIndex(j);
      // EKParticipantStatusDeclined is 3.
      if (p.isCurrentUser && Number(p.participantStatus) === 3) declined = true;
    }
    out.push({
      title: e.title.isNil() ? '' : e.title.js,
      location: e.location.isNil() ? '' : e.location.js,
      link: e.URL.isNil() ? '' : e.URL.absoluteString.js,
      people: people,
      declined: declined,
      start: e.startDate.timeIntervalSince1970 * 1000,
      end: e.endDate.timeIntervalSince1970 * 1000,
    });
  }
  return JSON.stringify({ status: st, events: out });
}
