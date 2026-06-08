/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{svelte,js,ts}'],
  theme: {
    extend: {}
  },
  plugins: [require('daisyui')],
  daisyui: {
    themes: [
      {
        night: {
          ...require('daisyui/src/theming/themes')['night'],
          primary: '#5b8af5',
          'primary-content': '#ffffff',
          secondary: '#ab47bc',
          accent: '#4a76e0',
          success: '#4caf50',
          warning: '#ff9800',
          error: '#ef5350'
        }
      }
    ],
    darkTheme: 'night'
  }
};
