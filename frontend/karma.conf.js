// Karma configuration file, see link for more information
// https://karma-runner.github.io/1.0/config/configuration-file.html

module.exports = function (config) {
  config.set({
    basePath: '',
    frameworks: ['jasmine'],
    plugins: [
      require('karma-jasmine'),
      require('karma-chrome-launcher'),
      require('karma-firefox-launcher'),
      require('karma-jasmine-html-reporter'),
      require('karma-coverage')
    ],
    client: {
      jasmine: {
        // you can add configuration options for Jasmine here
      },
      clearContext: false // leave Jasmine Spec Runner output visible in browser
    },
    jasmineHtmlReporter: {
      suppressAll: true // removes the duplicated traces
    },
    coverageReporter: {
      dir: require('path').join(__dirname, './coverage/frontend'),
      subdir: '.',
      reporters: [
        { type: 'html' },
        { type: 'text-summary' }
      ]
    },
    reporters: ['progress', 'kjhtml'],
    // N-D9: the FE-056 self-hosted fonts are referenced from the bundled CSS as
    // "./media/…", which the test context requests as "/base/media/…". Only the
    // "/media/…" build outputs are served (Angular's test-assets middleware), so
    // every font weight logged a 404 on each test run. Proxy the context-relative
    // path onto the served build output to silence them.
    proxies: {
      '/base/media/': '/media/'
    },
    // CI sets CHROME_BIN (see .github/workflows/ci.yml) and gets ChromeHeadless;
    // local development falls back to the Firefox headless launcher.
    browsers: [process.env.CHROME_BIN ? 'ChromeHeadless' : 'FirefoxHeadless'],
    customLaunchers: {
      FirefoxHeadless: {
        base: 'Firefox',
        flags: ['-headless']
      }
    },
    restartOnFileChange: true,
    singleRun: true
  });
};
