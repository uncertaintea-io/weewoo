import { expect } from 'chai';
import { JSDOM } from 'jsdom';
import 'mocha';
import { confirmAction } from './confirmation-dialog';

describe('confirmation dialog', () => {
  let dom: JSDOM;

  beforeEach(() => {
    dom = new JSDOM('<!doctype html><body><main id="app"><button id="page-action">Page action</button></main></body>');
    globalThis.document = dom.window.document;
    globalThis.HTMLElement = dom.window.HTMLElement;
    globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  });

  afterEach(() => {
    dom.window.close();
  });

  it('keeps keyboard focus in the dialog and restores the page when finished', async () => {
    const app = document.querySelector<HTMLElement>('#app');
    const pageAction = document.querySelector<HTMLElement>('#page-action');
    pageAction?.focus();

    const result = confirmAction({
      title: 'Delete service?',
      message: 'This cannot be undone.',
      confirmLabel: 'Delete service',
      dangerous: true,
    });

    const cancel = document.querySelector<HTMLButtonElement>('.confirmation-actions .secondary-button');
    const confirm = document.querySelector<HTMLButtonElement>('.confirmation-actions .danger-button');
    expect(app?.inert).to.equal(true);
    expect(document.activeElement).to.equal(confirm);

    confirm?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }));
    expect(document.activeElement).to.equal(cancel);

    cancel?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true }));
    expect(document.activeElement).to.equal(confirm);

    pageAction?.focus();
    pageAction?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }));
    expect(document.activeElement).to.equal(cancel);

    cancel?.click();
    expect(await result).to.equal(false);
    expect(app?.inert).to.equal(false);
    expect(document.activeElement).to.equal(pageAction);
  });
});
