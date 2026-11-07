import React from 'react';
import PropTypes from 'prop-types';
import ClockIcon from 'assets/icons/clock_icon.svg';
import TicketIcon from 'assets/icons/ticket_icon.svg';
import './styles/eventPopover.scss';

export default function EventPopover({
  title,
  venue,
  region,
  hours,
  mins,
  miles,
  ticketPrice,
  attendeeCount,
  labels = {}
}) {
  return (
    <div className='event-popover'>
      <div className='event-popover__body'>
        <div className='event-popover__body__title'>
          {title}
        </div>
        <div className='event-popover__body__venue'>
          {`${venue && region ? `${venue}, ${region}` : ''}`}
        </div>
        <div className='event-popover__body__pricing'>
          <span className='event-popover__body__pricing__label'>
            {''}
          </span>
          <span className='event-popover__body__pricing__amount'>
            {ticketPrice}
          </span>
          <span className='event-popover__body__pricing__unit'>
            {''}
          </span>
        </div>
        <div className='event-popover__body__logistics'>
          <img
            className='event-popover__body__logistics__clock-icon'
            src={ClockIcon}
            alt={labels['img.alt.clockIcon'] || ''}
          />
          <div className='event-popover__body__logistics__details'>
            {hours >= 1 && <span>{hours}{'hr'}{mins > 0 && ' '}</span>}
            {mins > 0 && <span>{mins}{' min'}</span>}{', '}
            {miles}{` ${labels['eventPopover.miles']}`}
          </div>
          <div className='event-popover__body__logistics__attendance'>
            <img
              className='event-popover__body__logistics__attendance__ticket-icon'
              src={TicketIcon}
              alt={labels['img.alt.ticketIcon'] || ''}
            />
            <div className='event-popover__body__logistics__attendance__details'>
              {0} {}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};

EventPopover.propTypes = {
  title: PropTypes.string,
  venue: PropTypes.string,
  region: PropTypes.string,
  hours: PropTypes.number,
  mins: PropTypes.number,
  miles: PropTypes.string,
  ticketPrice: PropTypes.string,
  attendeeCount: PropTypes.number,
  labels: PropTypes.object
};
