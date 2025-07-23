// Version Selector JavaScript
(function() {
    'use strict';

    // Version configuration
    const versions = {
        'latest': { name: 'Latest (develop)', url: '/' },
        'v0.1': { name: 'v0.1', url: '/v0.1/' },
        'v0.2': { name: 'v0.2', url: '/v0.2/' },
        'v0.3': { name: 'v0.3', url: '/v0.3/' },
        'v0.4': { name: 'v0.4', url: '/v0.4/' },
        'v0.5': { name: 'v0.5', url: '/v0.5/' }
    };

    // Get current version from URL path
    function getCurrentVersion() {
        const path = window.location.pathname;
        const versionMatch = path.match(/^\/v(\d+\.\d+)/);
        if (versionMatch) {
            return `v${versionMatch[1]}`;
        }
        return 'latest';
    }

    // Create version selector
    function createVersionSelector() {
        const currentVersion = getCurrentVersion();

        const selector = document.createElement('div');
        selector.className = 'version-selector';
        selector.innerHTML = `
            <div class="version-label">Version</div>
            <select id="version-select">
                ${Object.entries(versions).map(([key, version]) =>
                    `<option value="${key}" ${key === currentVersion ? 'selected' : ''}>${version.name}</option>`
                ).join('')}
            </select>
        `;

        // Add event listener
        const select = selector.querySelector('#version-select');
        select.addEventListener('change', function() {
            const selectedVersion = this.value;
            const targetUrl = versions[selectedVersion].url;

            // Navigate to selected version
            if (selectedVersion === 'latest') {
                window.location.href = targetUrl;
            } else {
                // For versioned docs, try to maintain the same page
                const currentPath = window.location.pathname;
                const versionPath = currentPath.replace(/^\/v\d+\.\d+/, '');
                window.location.href = targetUrl + versionPath.substring(1);
            }
        });

        return selector;
    }

    // Initialize when DOM is ready
    function init() {
        // Wait for mdBook to be ready
        if (document.readyState === 'loading') {
            document.addEventListener('DOMContentLoaded', init);
            return;
        }

        // Add version selector to the page
        const body = document.body;
        if (body) {
            const selector = createVersionSelector();
            body.appendChild(selector);
        }
    }

    // Start initialization
    init();
})();
