/* =====================================================================
   modules/i18n.js — Translation table + t() with placeholder substitution.

   Two locales ship in the box: en (default) and ar. Pages never
   inline strings in en directly; they go through t('key'). New
   keys can be added at the bottom of TABLE without rewriting
   pages. The base locale is loaded synchronously so the first
   paint is consistent; a future locale pack can be fetched
   lazily via /admin/i18n/<loc>.json and merged in.

   Pluralization: pass { n, one, many } as the only arg to get
   the count-aware variant. Other placeholder keys are substituted
   positionally or by name ('{name}' style).
   ===================================================================== */

const TABLE = {
  en: {
    'login.eyebrow':         'Administrator Access',
    'login.title':           'Sign in to Orvix Admin',
    'login.subtitle':        'Use the administrator credentials configured during installation.',
    'login.brandTitle':      'Orvix Mail Platform',
    'login.brandCopy':       'Admin control for domains, mailboxes, delivery queues, DNS, runtime health, and operational logs.',
    'login.buildTag':        '{version}',
    'login.username':        'Username',
    'login.password':        'Password',
    'login.usernamePh':      'admin@example.com',
    'login.passwordPh':      '\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022',
    'login.mfa':             'One-time code',
    'login.signIn':          'Sign In',
    'login.failed':          'Sign-in failed. Check your credentials.',

    'sidebar.dashboard':     'Dashboard',
    'sidebar.section.core':  'CORE',
    'sidebar.section.services': 'SERVICES',
    'sidebar.section.domains':  'DOMAINS & ACCOUNTS',
    'sidebar.section.security': 'SECURITY & FILTERING',
    'sidebar.section.updates':  'UPGRADES & UPDATES',
    'sidebar.section.queue':    'QUEUE',
    'sidebar.section.status':   'STATUS & MONITORING',
    'sidebar.section.logging':  'LOGGING',
    'sidebar.section.backup':   'BACKUP & RESTORE',
    'sidebar.section.migration': 'AUTOMATIC MIGRATION',
    'sidebar.section.clustering': 'CLUSTERING',
    'sidebar.section.admin':   'ADMINISTRATION',

    // ...groups
    'sidebar.group.globalSettings':  'Global Settings',
    'sidebar.group.services':        'Services',
    'sidebar.group.domainsAccounts': 'Domains & Accounts',
    'sidebar.group.security':        'Security & Filtering',
    'sidebar.group.updates':         'Upgrades & Updates',
    'sidebar.group.queue':           'Queue',
    'sidebar.group.status':          'Status & Monitoring',
    'sidebar.group.logging':         'Logging',
    'sidebar.group.backup':          'Backup & Restore',
    'sidebar.group.migration':       'Automatic Migration',
    'sidebar.group.clustering':      'Clustering',
    'sidebar.group.adminRights':     'Administration Rights',

    'sidebar.item.generalSettings': 'General Settings',
    'sidebar.item.securityDefaults': 'Security Defaults',
    'sidebar.item.license':        'License',
    'sidebar.item.buildInfo':      'Build / Runtime Info',
    'sidebar.item.services':       'Services Management',
    'sidebar.item.runtimeListeners': 'Runtime Listeners',
    'sidebar.item.domains':        'Manage Domains',
    'sidebar.item.accounts':       'Manage Accounts',
    'sidebar.item.groups':         'Groups',
    'sidebar.item.mailingLists':   'Mailing Lists',
    'sidebar.item.publicFolders':  'Public Folders',
    'sidebar.item.accountClasses': 'Account Classes',
    'sidebar.item.bulkImport':     'Bulk Mailbox Import',
    'sidebar.item.dnsDkim':        'DNS & DKIM',
    'sidebar.item.sslCerts':       'SSL Certificates',
    'sidebar.item.antivirus':      'Antivirus / Anti-spam',
    'sidebar.item.spamControl':    'Global Spam Control',
    'sidebar.item.routing':        'Acceptance & Routing',
    'sidebar.item.incomingRules':  'Incoming Message Rules',
    'sidebar.item.quarantine':     'View Quarantine',
    'sidebar.item.loginProtection':'Login Protection',
    'sidebar.group.protocolSettings': 'Protocol Settings',
    'sidebar.item.smtpRecv':        'SMTP Receiving',
    'sidebar.item.smtpTx':          'SMTP Sending',
    'sidebar.item.imap':            'IMAP',
    'sidebar.item.pop3':            'POP3',
    'sidebar.item.webmailS':        'WebMail',
    'sidebar.item.webadminS':       'WebAdmin',
    'sidebar.item.dnsProto':        'DNS',
    'sidebar.item.remotePop':       'Remote POP',
    'sidebar.item.jmap':            'JMAP',
    'sidebar.item.mobility':        'Mobility & Sync',
    'sidebar.item.updateStatus':   'Update Status',
    'sidebar.item.upgradeChecks':  'Upgrade Checks',
    'sidebar.item.queueProcessing':'Queue Processing',
    'sidebar.item.queueView':      'View Queue',
    'sidebar.item.reporting':      'Reporting Service',
    'sidebar.item.charts':         'Charts',
    'sidebar.item.storageCharts':  'Storage Charts',
    'sidebar.item.alertProviders': 'Alert Providers',
    'sidebar.item.runtimeListeners2': 'Runtime Listeners',
    'sidebar.item.localLogs':      'Local Service Logs',
    'sidebar.item.logRules':       'Log Collection Rules',
    'sidebar.item.viewLogFiles':   'View Log Files',
    'sidebar.item.logServer':      'Log Server Settings',
    'sidebar.item.backupStatus':   'Backup Status',
    'sidebar.item.backupHistory':  'Backup History',
    'sidebar.item.ftpBackup':      'FTP Backup & Restore',
    'sidebar.item.fsAccess':       'File System Access',
    'sidebar.item.migrationJobs':  'Migration Jobs',
    'sidebar.item.sourceServers':  'Source Servers',
    'sidebar.item.clusterSetup':   'Clustering Setup',
    'sidebar.item.imapProxy':      'IMAP Proxy',
    'sidebar.item.pop3Proxy':      'POP3 Proxy',
    'sidebar.item.webmailProxy':   'Webmail Proxy',
    'sidebar.item.adminGroups':    'Administrative Groups',
    'sidebar.item.adminUsers':    'Administrative Users',
    'sidebar.item.auditLog':       'Audit Log',
    'sidebar.item.domainAdminLimits': 'Domain Admin Limits',

    'topbar.subtitle':        'System command center',
    'topbar.refresh':         'Refresh',
    'topbar.signOut':         'Sign Out',

    'common.loading':         'Loading\u2026',
    'common.empty':           'No data',
    'common.search':          'Search\u2026',
    'common.cancel':          'Cancel',
    'common.confirm':         'Confirm',
    'common.close':           'Close',
    'common.back':            'Back',
    'common.delete':          'Delete',
    'common.edit':            'Edit',
    'common.save':            'Save',
    'common.required':        'Required',
    'common.optional':        'Optional',
    'common.ok':              'OK',
    'common.learnMore':       'Learn more',
    'common.planned':         'Planned',
    'common.disabled':        'Disabled',
    'common.readOnly':        'Read-only',
    'common.demo':           'Demo only',
    'common.copy':           'Copy',
    'common.copied':         'Copied',
    'common.copiedValue':    'Copied to clipboard',
    'common.copyFailed':     'Copy failed',
    'common.secret':         'Redacted',
    'common.lastUpdated':    'Last updated {when}',
    'common.never':          'Never',
    'common.error':          'Error',
    'common.warning':        'Warning',
    'common.info':           'Info',

    'dashboard.heading':      'Orvix Mail Platform',
    'dashboard.title':        'Operator Dashboard',
    'dashboard.system':       'System',
    'dashboard.services':     'Service Status',
    'dashboard.mailStats':    'Mail Statistics',
    'dashboard.warnings':     'Warnings & Recent Activity',

    'settings.heading':       'Settings',
    'settings.subtitle':      'Global settings and build information',

    'monitoring.heading':     'Monitoring',
    'monitoring.health':      'Component health',
    'monitoring.alerts':      'Active alerts',
    'monitoring.capacity':    'Capacity',
    'monitoring.resolve':     'Resolve',
    'monitoring.alertProviders': 'Alert Providers',

    'backups.heading':        'Backup & Restore',
    'backups.create':         'Create backup now',
    'backups.validate':       'Validate',
    'backups.restore':        'Restore',
    'backups.delete':        'Delete',
    'backups.restoreWarn':    'Restoring a backup replaces ALL current mailboxes and rules. Type the backup id to confirm.',

    'queue.heading':          'Queue',
    'queue.summary':          'Summary',
    'queue.status.all':       'All',
    'queue.status.queued':    'Queued',
    'queue.status.deferred':  'Deferred',
    'queue.status.failed':    'Failed',
    'queue.retry':            'Retry',
    'queue.cancel':           'Cancel',
    'queue.bounce':           'Bounce',

    'dns.heading':            'DNS & DKIM',
    'dns.check':              'Check',
    'dns.wizard':             'Wizard',
    'dns.apply':              'Apply',
    'dns.dkim.rotate':        'Rotate key',
    'dns.dkim.rotateWarn':    'Rotating the DKIM key invalidates the previous private key. Outbound mail continues to verify against the OLD key until DNS TTL expires.',
    'dns.copyRecord':         'Copy record',

    'bulk.heading':           'Bulk Mailbox Import',
    'bulk.paste':             'Paste CSV',
    'bulk.upload':            'Upload CSV',
    'bulk.template':          'Download template',
    'bulk.dryRun':            'Dry run',
    'bulk.import':            'Import',
    'bulk.partial':           'Allow partial (skip invalid rows)',
    'bulk.partialWarn':       'Enabling partial mode will commit the rows that pass validation and skip rows that fail. Failed rows are reported but the rest are committed.',
    'bulk.templateText':      'email,password,name,quota_mb',
    'bulk.passwordsHidden':   'Passwords cleared after upload',

    'runtime.heading':        'Runtime Listeners',
    'runtime.subtitle':       'Actual bind state for every configured listener. Skipped/degraded are not active.',
    'runtime.lastReload':     'Last refreshed',

    'license.heading':        'License',
    'license.tier':           'Tier',
    'license.seats':          'Seats',
    'license.expires':        'Expires',
    'license.offline':        'Offline',
    'license.publicKeyMissing':'Public key missing',
    'license.licenseMissing': 'License missing',
    'license.valid':          'Valid',
    'license.invalid':        'Invalid',
    'license.expired':        'Expired',
    'license.validate':       'Validate',

    'updates.heading':        'Updates',
    'updates.current':        'Current version',
    'updates.check':          'Check for updates',
    'updates.apply':          'Apply update',
    'updates.preflight':      'Pre-flight',

    'logs.heading':           'Logs',
    'logs.severity':          'Severity',
    'logs.source':            'Source',
    'logs.since':             'Since',

    'planned.feature':        'This feature is planned for a future release. Backend exposure is not yet wired; no fake endpoint is served.',
  },

  ar: {
    'login.eyebrow':         'صلاحيات الإدارة',
    'login.title':           'تسجيل الدخول إلى Orvix',
    'login.subtitle':        'استخدم بيانات اعتماد المسؤول التي تم إعدادها أثناء التثبيت.',
    'login.brandTitle':      'منصة Orvix للبريد',
    'login.brandCopy':       'تحكم إداري في النطاقات وصناديق البريد وقوائم التسليم وDNS وصحة التشغيل وسجلات العمليات.',
    'login.buildTag':        '{version}',
    'login.username':        'اسم المستخدم',
    'login.password':        'كلمة المرور',
    'login.usernamePh':      'admin@example.com',
    'login.passwordPh':      '\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022',
    'login.mfa':             'رمز المرة الواحدة',
    'login.signIn':          'تسجيل الدخول',
    'login.failed':          'فشل تسجيل الدخول. تحقق من بيانات الاعتماد.',

    'sidebar.dashboard':     'لوحة المعلومات',
    'sidebar.section.core':  'الأساس',
    'sidebar.section.services': 'الخدمات',
    'sidebar.section.domains':  'النطاقات والحسابات',
    'sidebar.section.security': 'الأمان والتصفية',
    'sidebar.section.updates':  'الترقيات والتحديثات',
    'sidebar.section.queue':    'قائمة الانتظار',
    'sidebar.section.status':   'الحالة والمراقبة',
    'sidebar.section.logging':  'السجلات',
    'sidebar.section.backup':   'النسخ الاحتياطي والاستعادة',
    'sidebar.section.migration': 'الترحيل التلقائي',
    'sidebar.section.clustering': 'التجميع',
    'sidebar.section.admin':   'الإدارة',

    'sidebar.group.globalSettings': 'الإعدادات العامة',
    'sidebar.group.services':       'إدارة الخدمات',
    'sidebar.group.domainsAccounts': 'النطاقات والحسابات',
    'sidebar.group.security':       'الأمان والتصفية',
    'sidebar.group.updates':        'الترقيات والتحديثات',
    'sidebar.group.queue':          'قائمة الانتظار',
    'sidebar.group.status':         'الحالة والمراقبة',
    'sidebar.group.logging':        'السجلات',
    'sidebar.group.backup':         'النسخ الاحتياطي والاستعادة',
    'sidebar.group.migration':      'الترحيل التلقائي',
    'sidebar.group.clustering':     'التجميع',
    'sidebar.group.adminRights':    'صلاحيات الإدارة',

    'sidebar.item.generalSettings': 'الإعدادات العامة',
    'sidebar.item.securityDefaults': 'إعدادات الأمان الافتراضية',
    'sidebar.item.license':        'الترخيص',
    'sidebar.item.buildInfo':      'معلومات الإصدار والتشغيل',
    'sidebar.item.services':       'إدارة الخدمات',
    'sidebar.item.runtimeListeners': 'حالة المستمعين',
    'sidebar.item.domains':        'إدارة النطاقات',
    'sidebar.item.accounts':       'إدارة الحسابات',
    'sidebar.item.groups':         'المجموعات',
    'sidebar.item.mailingLists':   'القوائم البريدية',
    'sidebar.item.publicFolders':  'المجلدات العامة',
    'sidebar.item.accountClasses': 'فئات الحسابات',
    'sidebar.item.bulkImport':     'استيراد صناديق البريد بالجملة',
    'sidebar.item.dnsDkim':        'DNS و DKIM',
    'sidebar.item.sslCerts':       'شهادات SSL',
    'sidebar.item.antivirus':      'مكافحة الفيروسات / السبام',
    'sidebar.item.spamControl':    'التحكم العام في السبام',
    'sidebar.item.routing':        'القبول والتوجيه',
    'sidebar.item.incomingRules':  'قواعد الرسائل الواردة',
    'sidebar.item.quarantine':     'عرض الحجر',
    'sidebar.item.loginProtection':'حماية تسجيل الدخول',
    'sidebar.group.protocolSettings': 'إعدادات البروتوكول',
    'sidebar.item.smtpRecv':        'استقبال SMTP',
    'sidebar.item.smtpTx':          'إرسال SMTP',
    'sidebar.item.imap':            'IMAP',
    'sidebar.item.pop3':            'POP3',
    'sidebar.item.webmailS':        'WebMail',
    'sidebar.item.webadminS':       'WebAdmin',
    'sidebar.item.dnsProto':        'DNS',
    'sidebar.item.remotePop':       'POP البعيد',
    'sidebar.item.jmap':            'JMAP',
    'sidebar.item.mobility':        'المزامنة',
    'sidebar.item.updateStatus':   'حالة التحديث',
    'sidebar.item.upgradeChecks':  'فحص الترقية',
    'sidebar.item.queueProcessing':'معالجة قائمة الانتظار',
    'sidebar.item.queueView':      'عرض قائمة الانتظار',
    'sidebar.item.reporting':      'خدمة التقارير',
    'sidebar.item.charts':         'الرسوم البيانية',
    'sidebar.item.storageCharts':  'رسوم التخزين',
    'sidebar.item.alertProviders': 'مزودو التنبيهات',
    'sidebar.item.localLogs':      'سجلات الخدمة المحلية',
    'sidebar.item.logRules':       'قواعد تجميع السجلات',
    'sidebar.item.viewLogFiles':   'عرض ملفات السجل',
    'sidebar.item.logServer':      'إعدادات خادم السجل',
    'sidebar.item.backupStatus':   'حالة النسخ الاحتياطي',
    'sidebar.item.backupHistory':  'سجل النسخ الاحتياطية',
    'sidebar.item.ftpBackup':      'النسخ الاحتياطي والاستعادة عبر FTP',
    'sidebar.item.fsAccess':       'الوصول إلى نظام الملفات',
    'sidebar.item.migrationJobs':  'مهام الترحيل',
    'sidebar.item.sourceServers':  'الخوادم المصدر',
    'sidebar.item.clusterSetup':   'إعداد التجميع',
    'sidebar.item.imapProxy':      'وكيل IMAP',
    'sidebar.item.pop3Proxy':      'وكيل POP3',
    'sidebar.item.webmailProxy':   'وكيل الويب ميل',
    'sidebar.item.adminGroups':    'مجموعات الإدارة',
    'sidebar.item.adminUsers':    'المستخدمون الإداريون',
    'sidebar.item.auditLog':       'سجل التدقيق',
    'sidebar.item.domainAdminLimits': 'حدود مسؤولي النطاق',

    'topbar.subtitle':        'مركز قيادة النظام',
    'topbar.refresh':         'تحديث',
    'topbar.signOut':         'تسجيل الخروج',

    'common.loading':         'جارٍ التحميل\u2026',
    'common.empty':           'لا توجد بيانات',
    'common.search':          'بحث\u2026',
    'common.cancel':          'إلغاء',
    'common.confirm':         'تأكيد',
    'common.close':           'إغلاق',
    'common.back':            'رجوع',
    'common.delete':          'حذف',
    'common.edit':            'تحرير',
    'common.save':            'حفظ',
    'common.required':        'مطلوب',
    'common.optional':        'اختياري',
    'common.ok':              'موافق',
    'common.learnMore':       'اعرف المزيد',
    'common.planned':         'مخطط له',
    'common.disabled':        'معطل',
    'common.readOnly':        'للقراءة فقط',
    'common.demo':           'للعرض فقط',
    'common.copy':           'نسخ',
    'common.copied':         'تم النسخ',
    'common.copiedValue':    'تم النسخ إلى الحافظة',
    'common.copyFailed':     'فشل النسخ',
    'common.secret':         'محجوب',
    'common.lastUpdated':    'آخر تحديث {when}',
    'common.never':          'أبداً',
    'common.error':          'خطأ',
    'common.warning':        'تحذير',
    'common.info':           'معلومة',

    'dashboard.heading':      'منصة Orvix للبريد',
    'dashboard.title':        'لوحة المشغل',
    'dashboard.system':       'النظام',
    'dashboard.services':     'حالة الخدمات',
    'dashboard.mailStats':    'إحصائيات البريد',
    'dashboard.warnings':     'التحذيرات والنشاط الأخير',

    'settings.heading':       'الإعدادات',
    'settings.subtitle':      'الإعدادات العامة ومعلومات الإصدار',

    'monitoring.heading':     'المراقبة',
    'monitoring.health':      'صحة المكونات',
    'monitoring.alerts':      'التنبيهات النشطة',
    'monitoring.capacity':    'السعة',
    'monitoring.resolve':     'حل',
    'monitoring.alertProviders': 'مزودو التنبيهات',

    'backups.heading':        'النسخ الاحتياطي والاستعادة',
    'backups.create':         'إنشاء نسخة احتياطية الآن',
    'backups.validate':       'تحقق',
    'backups.restore':        'استعادة',
    'backups.delete':        'حذف',
    'backups.restoreWarn':    'استعادة النسخة الاحتياطية تستبدل جميع صناديق البريد والقواعد الحالية. اكتب معرف النسخة الاحتياطية للتأكيد.',

    'queue.heading':          'قائمة الانتظار',
    'queue.summary':          'الملخص',
    'queue.status.all':       'الكل',
    'queue.status.queued':    'في الانتظار',
    'queue.status.deferred':  'مؤجل',
    'queue.status.failed':    'فشل',
    'queue.retry':            'إعادة',
    'queue.cancel':           'إلغاء',
    'queue.bounce':           'ارتداد',

    'dns.heading':            'DNS و DKIM',
    'dns.check':              'فحص',
    'dns.wizard':             'معالج',
    'dns.apply':              'تطبيق',
    'dns.dkim.rotate':        'تدوير المفتاح',
    'dns.dkim.rotateWarn':    'تدوير مفتاح DKIM يُبطل المفتاح الخاص السابق. يستمر التحقق من البريد الصادر باستخدام المفتاح القديم حتى انتهاء TTL في DNS.',
    'dns.copyRecord':         'نسخ السجل',

    'bulk.heading':           'استيراد صناديق البريد بالجملة',
    'bulk.paste':             'لصق CSV',
    'bulk.upload':            'تحميل CSV',
    'bulk.template':          'تنزيل القالب',
    'bulk.dryRun':            'تشغيل جاف',
    'bulk.import':            'استيراد',
    'bulk.partial':           'السماح بالجزئي (تخطي الصفوف غير الصالحة)',
    'bulk.partialWarn':       'تمكين الوضع الجزئي سيلتزم بالصفوف التي تجتاز التحقق ويتخطى الفاشلة. يتم الإبلاغ عن الصفوف الفاشلة ولكن الباقي يلتزم.',
    'bulk.templateText':      'email,password,name,quota_mb',
    'bulk.passwordsHidden':   'تم مسح كلمات المرور بعد التحميل',

    'runtime.heading':        'حالة المستمعين',
    'runtime.subtitle':       'حالة الربط الفعلية لكل مستمع مُكوَّن. المتخطي/المتدهور ليس نشطاً.',
    'runtime.lastReload':     'آخر تحديث',

    'license.heading':        'الترخيص',
    'license.tier':           'الفئة',
    'license.seats':          'المقاعد',
    'license.expires':        'ينتهي في',
    'license.offline':        'دون اتصال',
    'license.publicKeyMissing':'المفتاح العام مفقود',
    'license.licenseMissing': 'الترخيص مفقود',
    'license.valid':          'صالح',
    'license.invalid':        'غير صالح',
    'license.expired':        'منتهي',
    'license.validate':       'تحقق',

    'updates.heading':        'التحديثات',
    'updates.current':        'الإصدار الحالي',
    'updates.check':          'البحث عن تحديثات',
    'updates.apply':          'تطبيق التحديث',
    'updates.preflight':      'فحص مسبق',

    'logs.heading':           'السجلات',
    'logs.severity':          'الخطورة',
    'logs.source':            'المصدر',
    'logs.since':             'منذ',

    'planned.feature':        'هذه الميزة مخطط لها في إصدار مستقبلي. الربط الخلفي لم يُضبط بعد؛ لا توجد نقطة نهاية وهمية.',
  },
};

let _locale = 'en';
let _listeners = [];

export function getLocale() { return _locale; }
export function setLocale(loc) {
  _locale = TABLE[loc] ? loc : 'en';
  _listeners.forEach((cb) => { try { cb(_locale); } catch (_) {} });
}
export function onLocaleChange(cb) { _listeners.push(cb); return () => { _listeners = _listeners.filter((x) => x !== cb); }; }
export function knownLocales() { return Object.keys(TABLE); }

/**
 * t(key, params?) returns the localized string. Falls back to the
 * key itself if the key is missing in the active locale (and to
 * English if it's also missing there). Params are substituted into
 * '{name}' slots in the localized text.
 */
export function t(key, params) {
  const loc = TABLE[_locale] || TABLE.en;
  let s = loc[key];
  if (s == null) s = TABLE.en[key] != null ? TABLE.en[key] : key;
  if (params && typeof s === 'string') {
    Object.keys(params).forEach((k) => {
      s = s.replace(new RegExp('\\{' + k + '\\}', 'g'), String(params[k]));
    });
  }
  return s;
}

/**
 * tPlural(key, n, vars?) returns a count-aware variant. The TABLE
 * may include both 'foo' (key) and 'foo.plural' (variant) keys.
 * We do not implement full CLDR plural rules; a simple binary
 * split is enough for our copy.
 */
export function tPlural(key, n, vars) {
  const altKey = n === 1 ? key : (key + '.plural');
  let s = t(altKey, vars);
  if (s === altKey) s = t(key, vars); // fall back to base
  return s.replace('{n}', String(n));
}

// One-time init: read ?lang=... in the URL and persist.
export function initLocaleFromURL() {
  try {
    const params = new URLSearchParams(window.location.search);
    const want = params.get('lang') || localStorage.getItem('orvix_locale') || navigator.language || 'en';
    const loc = (want || '').toLowerCase().startsWith('ar') ? 'ar' : 'en';
    setLocale(loc);
    document.documentElement.lang = loc;
    try { localStorage.setItem('orvix_locale', loc); } catch (_) {}
  } catch (_) {
    setLocale('en');
  }
}
