// jest.config.js
module.exports = {
    preset: 'ts-jest',
    testEnvironment: 'node',
    extensionsToTreatAsEsm: ['.ts'],
    transform: {
      '^.+\\.tsx?$': 'babel-jest',
    },
    moduleNameMapper: {
      '^(.*)\\.js$': '$1',
    },
    testRunner: "jest-circus/runner",
    testTimeout: 120000,
    maxConcurrency: 1,
  };
