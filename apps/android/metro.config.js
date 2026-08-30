const { getDefaultConfig, mergeConfig } = require('@react-native/metro-config');

/**
 * Metro configuration
 * https://reactnative.dev/docs/metro
 *
 * @type {import('@react-native/metro-config').MetroConfig}
 */
const config = {
  transformer: {
    // Disable JS minification so feature-flagged code (e.g. cloud management)
    // is never tree-shaken out of production-mode bundles.
    minifierConfig: {
      // No minifier -> code is kept as-is (only constant folding, no DCE of branches)
    },
  },
};

module.exports = mergeConfig(getDefaultConfig(__dirname), config);
