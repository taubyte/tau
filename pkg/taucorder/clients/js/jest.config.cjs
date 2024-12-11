// jest.config.js
module.exports = {
    preset: 'ts-jest',
    testEnvironment: 'node',
    extensionsToTreatAsEsm: ['.ts'],
    transform: {
      '^.+\\.tsx?$': 'babel-jest',  // Use babel-jest for TypeScript files
    },
    moduleNameMapper: {
      // Map all imports ending with .js to .ts files
      '^(.*)\\.js$': '$1',
    },
    testRunner: 'jest-circus/runner',  // Optional: use modern test runner
  };
  