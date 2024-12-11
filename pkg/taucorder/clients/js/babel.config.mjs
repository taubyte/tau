export default {
    presets: [
      ['@babel/preset-env', { targets: { node: 'current' } }], // Target Node.js environment
      '@babel/preset-typescript',  // Add TypeScript support
    ],
    plugins: ['@babel/plugin-syntax-import-meta'],  // Add support for import.meta
  };
  