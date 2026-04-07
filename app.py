const DelegatarrUI = {
    init() {
        this.cacheDOM();
        this.bindEvents();
        this.initServiceWorker();
        this.initFlashMessages();
        this.initTimezoneSelect();
		this.initWizard();
        this.initSearch();
        this.initFilters();
        this.initSorting();
    },

    cacheDOM() {
        this.htmlRoot = document.documentElement;
        this.sidebarToggle = document.getElementById('sidebarToggle');
        this.mobileOverlay = document.getElementById('mobileOverlay');
        this.trackerFilter = document.getElementById('trackerFilter');
        this.trackerSearch = document.getElementById('trackerSearch');
        this.trackerTable = document.getElementById('trackerTable');
        this.trackerCount = document.getElementById('trackerCount');
        this.rulesSearch = document.getElementById('rulesSearch');
        this.rulesTable = document.getElementById('rulesTable');
        this.rulesCount = document.getElementById('rulesCount');
        this.fileInput = document.getElementById('settingsUpload');
        this.fileNameDisplay = document.getElementById('fileNameDisplay');
        this.tzSelect = document.getElementById('tzSelect');
    },

    bindEvents() {
        if (this.sidebarToggle) {
            this.sidebarToggle.addEventListener('click', () => this.toggleSidebar());
        }
        
        if (this.mobileOverlay) {
            this.mobileOverlay.addEventListener('click', () => {
                this.htmlRoot.classList.remove('mobile-sidebar-open');
            });
        }

        if (this.trackerFilter) {
            this.trackerFilter.addEventListener('change', () => this.filterTrackers());
        }

        if (this.fileInput && this.fileNameDisplay) {
            this.fileInput.addEventListener('change', (e) => {
                this.fileNameDisplay.textContent = e.target.files.length > 0 ? e.target.files[0].name : 'No file chosen';
            });
        }

        document.querySelectorAll('form[data-confirm-word]').forEach(form => {
            form.addEventListener('submit', (e) => {
                const expectedWord = form.dataset.confirmWord;
                const message = form.dataset.confirmMsg || `Type ${expectedWord} to confirm:`;
                
                const userInput = prompt(message);
                
                if (userInput !== expectedWord) {
                    e.preventDefault();
                }
            });
        });
    },

    toggleSidebar() {
        if (window.innerWidth <= 768) {
            this.htmlRoot.classList.toggle('mobile-sidebar-open');
        } else {
            this.htmlRoot.classList.toggle('sidebar-collapsed');
            sessionStorage.setItem('sidebarCollapsed', this.htmlRoot.classList.contains('sidebar-collapsed'));
        }
    },

    initServiceWorker() {
        // Now using the global config object passed from base.html
        if ('serviceWorker' in navigator && window.APP_CONFIG && window.APP_CONFIG.swUrl) {
            navigator.serviceWorker.register(window.APP_CONFIG.swUrl)
                .catch(err => console.warn('Service Worker registration failed:', err));
        }
    },

    initFlashMessages() {
        const flashes = document.querySelectorAll('.flash:not(.error)');
        if (flashes.length > 0) {
            setTimeout(() => {
                flashes.forEach(flash => {
                    flash.style.transition = 'opacity 0.5s ease, transform 0.5s ease';
                    flash.style.opacity = '0';
                    flash.style.transform = 'translateY(-10px)';
                    setTimeout(() => flash.remove(), 500); 
                });
            }, 4000);
        }
    },

    initTimezoneSelect() {
        if (!this.tzSelect) return;
        
        const timeZones = (typeof Intl !== 'undefined' && Intl.supportedValuesOf) 
            ? Intl.supportedValuesOf('timeZone') 
            : ['UTC'];
            
        const currentTz = this.tzSelect.dataset.currentTz || 'UTC';
        
        timeZones.forEach(tz => {
            const option = document.createElement('option');
            option.value = tz;
            option.textContent = tz;
            if (tz === currentTz) option.selected = true;
            this.tzSelect.appendChild(option);
        });
    },

    initSearch() {
        if (this.trackerSearch && this.trackerTable) {
            this.setupTableSearch(this.trackerSearch, this.trackerTable, this.trackerCount, false);
        }
        if (this.rulesSearch && this.rulesTable) {
            this.setupTableSearch(this.rulesSearch, this.rulesTable, this.rulesCount, true);
        }
    },

    initFilters() {
        if (this.trackerFilter) {
            const savedFilter = localStorage.getItem('trackerFilterPref');
            if (savedFilter) this.trackerFilter.value = savedFilter;
            this.filterTrackers(); 
        }
    },

    initSorting() {
        document.querySelectorAll('th.sortable').forEach(th => {
            th.dataset.sortOrder = 'desc'; 
            th.addEventListener('click', (e) => this.handleSortClick(e.currentTarget));
        });

        [this.trackerTable, this.rulesTable].forEach(table => {
            if (table && table.id) {
                const savedColText = localStorage.getItem(table.id + '_sortColText');
                const savedAsc = localStorage.getItem(table.id + '_sortAsc');
                
                if (savedColText !== null && savedAsc !== null) {
                    const isAsc = savedAsc === 'true';
                    const headers = Array.from(table.querySelectorAll('th.sortable'));
                    const targetTh = headers.find(th => th.textContent.trim() === savedColText);
                    
                    if (targetTh) {
                        const colIdx = Array.from(targetTh.parentNode.children).indexOf(targetTh);
                        targetTh.dataset.sortOrder = isAsc ? 'asc' : 'desc';
                        this.performSort(table, colIdx, isAsc);
                    }
                }
            }
        });
    },
	initWizard() {
        const form = document.getElementById('ruleWizardForm');
        if (!form) return;

        const steps = form.querySelectorAll('.wizard-step');
        const progressBar = document.getElementById('wizardBar');
        const nextBtns = form.querySelectorAll('.btn-next');
        const prevBtns = form.querySelectorAll('.btn-prev');

        const updateWizard = (targetStepNum) => {
            // Basic validation before moving forward
            const currentActive = form.querySelector('.wizard-step.active');
            const inputs = currentActive.querySelectorAll('input[required]');
            let isValid = true;
            inputs.forEach(input => {
                if (!input.value.trim()) {
                    input.style.borderColor = 'var(--danger)';
                    isValid = false;
                } else {
                    input.style.borderColor = 'var(--border-color)';
                }
            });

            if (!isValid && targetStepNum > parseInt(currentActive.id.split('-')[1])) return;

            // Hide all, show target
            steps.forEach(step => step.classList.remove('active'));
            document.getElementById(`step-${targetStepNum}`).classList.add('active');

            // Update progress bar (3 steps total)
            const progressObj = { 1: '33%', 2: '66%', 3: '100%' };
            progressBar.style.width = progressObj[targetStepNum];
        };

        nextBtns.forEach(btn => btn.addEventListener('click', (e) => {
            updateWizard(parseInt(e.target.dataset.next));
        }));

        prevBtns.forEach(btn => btn.addEventListener('click', (e) => {
            updateWizard(parseInt(e.target.dataset.prev));
        }));
    },

    debounce(func, wait) {
        let timeout;
        return (...args) => {
            clearTimeout(timeout);
            timeout = setTimeout(() => func(...args), wait);
        };
    },

    filterTrackers() {
        if (!this.trackerFilter || !this.trackerTable) return;
        const filter = this.trackerFilter.value;
        localStorage.setItem('trackerFilterPref', filter);
        
        const rows = this.trackerTable.querySelectorAll('.tracker-row');
        rows.forEach(row => {
            const input = row.querySelector('.tag-input');
            if (!input) return;
            const isTagged = input.value.trim() !== '';
            
            let shouldHide = false;
            if (filter === 'tagged' && !isTagged) shouldHide = true;
            else if (filter === 'untagged' && isTagged) shouldHide = true;
            
            row.dataset.filteredOut = shouldHide ? 'true' : 'false';
        });
        
        this.triggerSearch(this.trackerSearch, this.trackerTable, this.trackerCount);
    },

    setupTableSearch(searchInput, table, countElement, runImmediately = true) {
        if (!searchInput) return;
        searchInput.addEventListener('input', this.debounce(() => {
            this.triggerSearch(searchInput, table, countElement);
        }, 250));
        
        if (runImmediately) {
            this.triggerSearch(searchInput, table, countElement);
        }
    },

    triggerSearch(searchInput, table, countElement) {
        if (!searchInput || !table) return;

        const filter = searchInput.value.toLowerCase();
        const tbody = table.querySelector('tbody') || table;
        const rows = tbody.querySelectorAll('tr');

        let visibleCount = 0;
        let filteredTotalCount = 0;

        rows.forEach(row => {
            if (row.querySelector('th')) return; 
            if (row.dataset.placeholder === 'true') return;
            
            const isHiddenByDropdown = row.dataset.filteredOut === 'true';
            if (!isHiddenByDropdown) filteredTotalCount++;
            
            const textCells = Array.from(row.cells).filter(cell => !cell.querySelector('input'));
            let text = textCells.map(c => c.textContent).join(' ').toLowerCase();
            row.querySelectorAll('input:not([type="hidden"])').forEach(input => text += ' ' + input.value.toLowerCase());

            if (text.includes(filter) && !isHiddenByDropdown) {
                row.style.display = '';
                visibleCount++;
            } else {
                row.style.display = 'none';
            }
        });

        if (countElement) {
            if (filteredTotalCount === 0) {
                countElement.textContent = '';
            } else if (filter === '') {
                countElement.textContent = `Showing ${filteredTotalCount} total`;
            } else {
                countElement.textContent = `Showing ${visibleCount} of ${filteredTotalCount}`;
            }
        }
    },

    getCellValue(tr, idx) {
        const cell = tr.children[idx];
        const input = cell.querySelector('input');
        if (input) return input.value.trim();
        return (cell.innerText || cell.textContent).trim();
    },

    comparer(idx, asc) {
        return (a, b) => {
            const v1 = this.getCellValue(asc ? a : b, idx);
            const v2 = this.getCellValue(asc ? b : a, idx);
            
            if (v1 !== '' && v2 !== '' && !isNaN(v1) && !isNaN(v2)) {
                return v1 - v2;
            }
            return v1.toString().localeCompare(v2, undefined, {numeric: true, sensitivity: 'base'});
        };
    },

    handleSortClick(th) {
        const table = th.closest('table');
        const columnIndex = Array.from(th.parentNode.children).indexOf(th);
        const headerText = th.textContent.trim();
        
        const isAsc = th.dataset.sortOrder !== 'asc';
        th.dataset.sortOrder = isAsc ? 'asc' : 'desc';

        this.performSort(table, columnIndex, isAsc);

        if (table.id) {
            localStorage.setItem(table.id + '_sortColText', headerText);
            localStorage.setItem(table.id + '_sortAsc', isAsc);
        }
    },

    performSort(table, columnIndex, asc) {
        const tbody = table.querySelector('tbody') || table;
        const rows = Array.from(tbody.querySelectorAll('tr')).filter(tr => !tr.querySelector('th'));

        rows.sort(this.comparer(columnIndex, asc)).forEach(tr => tbody.appendChild(tr));

        table.querySelectorAll('th.sortable').forEach(th => {
            th.classList.remove('sorted-asc', 'sorted-desc');
        });
        
        const activeTh = table.querySelector(`th:nth-child(${columnIndex + 1})`);
        if (activeTh) activeTh.classList.add(asc ? 'sorted-asc' : 'sorted-desc');
    }
};

document.addEventListener('DOMContentLoaded', () => DelegatarrUI.init());
