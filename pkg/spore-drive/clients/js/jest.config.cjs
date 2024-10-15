// jest.config.js
module.exports = {
    preset: 'ts-jest',
    testEnvironment: 'node',
    moduleNameMapper: {
      // Map all imports ending with .js to .ts files
      '^(.*)\\.js$': '$1',
    },
  };
  